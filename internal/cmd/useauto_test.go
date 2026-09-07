package cmd

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/backup"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/lock"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/secret"
	"github.com/webkaz-labs/kagikae/internal/state"
)

func TestAutoUsePreservesSelectionAndManualBareClearsIt(t *testing.T) {
	for _, dry := range []bool{false, true} {
		t.Run(map[bool]string{false: "apply", true: "preview"}[dry], func(t *testing.T) {
			app := applyTestApp(t, nil)
			ctx := context.Background()
			opts := commonOpts{Format: formatJSON}
			code, out := captureStdout(t, func() int { return runUseIsolated(ctx, app, opts, "claude", "main") })
			mustExit(t, 0, code, out)
			fragment := readFile(t, app.Paths.MiseGlobalFragmentFile())
			live := readFile(t, app.Env.Home+"/.claude/.credentials.json")
			before := readFile(t, app.Paths.StateFile())
			backups, _ := backup.List(app.Paths.BackupsDir())
			opts.DryRun = dry
			code, out = captureStdout(t, func() int { return runUseAuto(ctx, app, opts, "side", false) })
			mustExit(t, 0, code, out)
			report := decodeBareUseReport(t, out)
			if report.Changed || len(report.Preserved) != 1 || report.Preserved[0].Account != "main" || len(report.Results) != 0 {
				t.Fatalf("wrong retained report: %s", out)
			}
			if readFile(t, app.Paths.StateFile()) != before || readFile(t, app.Paths.MiseGlobalFragmentFile()) != fragment || readFile(t, app.Env.Home+"/.claude/.credentials.json") != live {
				t.Fatal("auto rewrote retained selection")
			}
			after, _ := backup.List(app.Paths.BackupsDir())
			if !reflect.DeepEqual(backups, after) {
				t.Fatal("auto backed up an unchanged tool")
			}
			code, out = captureStdout(t, func() int { return runUseBare(ctx, app, opts, false, "side", false) })
			mustExit(t, 0, code, out)
			if !decodeBareUseReport(t, out).Changed {
				t.Fatal("manual shared selection did not tear down matching active account")
			}
			st, _ := app.loadState()
			if !dry && len(st.Synced) != 0 {
				t.Fatal("manual shared left isolation active")
			}
			if dry && readFile(t, app.Paths.MiseGlobalFragmentFile()) != fragment {
				t.Fatal("preview changed fragment")
			}
		})
	}
}

func TestAutoUseMixedProfileAppliesOnlySharedTargets(t *testing.T) {
	app := applyTestApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatJSON}
	seedCodex(t, app, "codex-main-token")
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "codex", "main") })
	mustExit(t, 0, code, out)
	app.Config.Profiles["main"].Accounts[constants.ToolCodex] = "main"
	code, out = captureStdout(t, func() int { return runUseIsolated(ctx, app, opts, "claude", "side") })
	mustExit(t, 0, code, out)
	_, err := app.mutateState(func(st *state.State) { delete(st.Active, constants.ToolCodex) })
	if err != nil {
		t.Fatal(err)
	}
	fragment := readFile(t, app.Paths.MiseGlobalFragmentFile())
	code, out = captureStdout(t, func() int { return runUseAuto(ctx, app, opts, "main", false) })
	mustExit(t, 0, code, out)
	r := decodeBareUseReport(t, out)
	if len(r.Preserved) != 1 || r.Preserved[0].Account != "side" || len(r.Results) != 1 || r.Results[0].Tool != constants.ToolCodex {
		t.Fatalf("bad mixed report: %s", out)
	}
	meta, err := backup.Get(app.Paths.BackupsDir(), r.BackupID)
	if err != nil || !reflect.DeepEqual(meta.Tools, []string{constants.ToolCodex}) {
		t.Fatalf("backup targets: %v %v", meta.Tools, err)
	}
	if readFile(t, app.Paths.MiseGlobalFragmentFile()) != fragment {
		t.Fatal("mixed apply rewrote fragment")
	}
}

func TestAutoUseRefusesMissingIsolationAndBusyWriter(t *testing.T) {
	for _, kind := range []string{"fragment", "home", "lock"} {
		t.Run(kind, func(t *testing.T) {
			app := applyTestApp(t, nil)
			ctx := context.Background()
			opts := commonOpts{Format: formatJSON}
			code, out := captureStdout(t, func() int { return runUseIsolated(ctx, app, opts, "claude", "main") })
			mustExit(t, 0, code, out)
			want := constants.ExitUnsafeRefused
			switch kind {
			case "fragment":
				writeFile(t, app.Paths.MiseGlobalFragmentFile(), "invalid")
			case "home":
				if err := os.Rename(app.Paths.GlobalIsolatedHomeDir("claude", "main"), app.Paths.GlobalIsolatedHomeDir("claude", "main")+"-away"); err != nil {
					t.Fatal(err)
				}
			case "lock":
				l, err := lock.Acquire(app.Paths.LocksDir(), isolationLifecycleLockName("claude"))
				if err != nil {
					t.Fatal(err)
				}
				defer l.Release()
				want = constants.ExitLockBusy
			}
			before := readFile(t, app.Paths.StateFile())
			code, out = captureStdout(t, func() int { return runUseAuto(ctx, app, opts, "side", true) })
			mustExit(t, want, code, out)
			if readFile(t, app.Paths.StateFile()) != before {
				t.Fatal("refusal changed state")
			}
		})
	}
}

func TestAutoUseArgumentExclusions(t *testing.T) {
	for _, args := range [][]string{{"--auto", "-s"}, {"--auto", "--shared=false"}, {"--auto", "--isolated=false"}, {"--auto", "-i"}, {"--auto", "main"}, {"--auto", "claude", "main"}} {
		code, out := captureStdout(t, func() int { return CmdUse(context.Background(), args) })
		mustExit(t, constants.ExitUsage, code, out)
	}
	if !strings.Contains(strings.Join(flagCompletions("use"), " "), "--auto") {
		t.Fatal("completion misses auto")
	}
}

func TestAutoUseBoundDirectoryKeepsLocalAndGlobalSelections(t *testing.T) {
	app := applyTestApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatJSON}
	code, out := captureStdout(t, func() int { return runUseIsolated(ctx, app, opts, "claude", "side") })
	mustExit(t, 0, code, out)
	global := readFile(t, app.Paths.MiseGlobalFragmentFile())
	dir := pinHereAs(t, app, "main", modeShared)
	localPath := filepath.Join(dir, ".config/mise/conf.d/kagikae.toml")
	local := readFile(t, localPath)
	localHome := app.Paths.SharedDir(paths.PinID(dir), constants.ToolClaude)
	if !strings.Contains(local, localHome) || !strings.Contains(local, "CLAUDE_CONFIG_DIR") || !strings.Contains(global, app.Paths.GlobalIsolatedHomeDir("claude", "side")) {
		t.Fatal("fixture must have distinct local and global environment selections")
	}
	originalGetenv := app.Env.Getenv
	app.Env.Getenv = func(key string) string {
		switch key {
		case constants.EnvKaeProfile:
			return "main"
		case "CLAUDE_CONFIG_DIR":
			return localHome
		}
		return originalGetenv(key)
	}
	app.globalScope = false
	code, out = captureStdout(t, func() int { return runUseAuto(ctx, app, opts, "", false) })
	mustExit(t, 0, code, out)
	if r := decodeBareUseReport(t, out); r.Changed || len(r.Preserved) != 1 || r.Preserved[0].Account != "side" {
		t.Fatalf("bound directory changed global choice: %s", out)
	}
	if readFile(t, localPath) != local || readFile(t, app.Paths.MiseGlobalFragmentFile()) != global {
		t.Fatal("hook changed a binding fragment")
	}
	app.Env.Getenv = originalGetenv
	app.globalScope = false
	code, out = captureStdout(t, func() int { return runUseAuto(ctx, app, opts, "main", false) })
	mustExit(t, 0, code, out)
	if readFile(t, app.Paths.MiseGlobalFragmentFile()) != global {
		t.Fatal("leaving directory changed global choice")
	}
}

func TestAutoUseQuietAndTextDescribeRetention(t *testing.T) {
	app := applyTestApp(t, nil)
	ctx := context.Background()
	code, out := captureStdout(t, func() int { return runUseIsolated(ctx, app, commonOpts{Format: formatJSON}, "claude", "side") })
	mustExit(t, 0, code, out)
	for _, quiet := range []bool{false, true} {
		code, out = captureStdout(t, func() int { return runUseAuto(ctx, app, commonOpts{Format: formatText}, "main", quiet) })
		mustExit(t, 0, code, out)
		if quiet && out != "" {
			t.Fatal("quiet printed success")
		}
		if !quiet && (!strings.Contains(out, "Preserved global isolated claude -> side") || strings.Contains(out, "main already active")) {
			t.Fatalf("misleading retained report: %s", out)
		}
	}
}

func TestAutoUseSharedPreviewDoesNotClaimItSwitched(t *testing.T) {
	app := applyTestApp(t, nil)
	before := readFile(t, app.Paths.StateFile())
	code, out := captureStdout(t, func() int {
		return runUseAuto(context.Background(), app, commonOpts{Format: formatText, DryRun: true}, "main", false)
	})
	mustExit(t, 0, code, out)
	if !strings.Contains(out, "Would switch") || strings.Contains(out, "Switched") || readFile(t, app.Paths.StateFile()) != before {
		t.Fatalf("incorrect preview: %s", out)
	}
}

type stateLockingBackend struct {
	secret.Backend
	app  *App
	held *lock.Lock
}

func (b *stateLockingBackend) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if b.held == nil {
		var err error
		b.held, err = lock.Acquire(b.app.Paths.LocksDir(), lockNameState)
		if err != nil {
			return nil, false, err
		}
	}
	return b.Backend.Get(ctx, key)
}

func TestAutoUseMixedRecordingFailureRestoresSharedStore(t *testing.T) {
	app := applyTestApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatJSON}
	seedCodex(t, app, "codex-main-token")
	code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "codex", "main") })
	mustExit(t, 0, code, out)
	seedCodex(t, app, "codex-side-token")
	code, out = captureStdout(t, func() int { return runCapture(ctx, app, opts, "codex", "side") })
	mustExit(t, 0, code, out)
	app.Config.Profiles["main"].Accounts[constants.ToolCodex] = "main"
	code, out = captureStdout(t, func() int { return runUseIsolated(ctx, app, opts, "claude", "side") })
	mustExit(t, 0, code, out)
	before := readFile(t, app.Env.Home+"/.codex/auth.json")
	fragment := readFile(t, app.Paths.MiseGlobalFragmentFile())
	beforeState := readFile(t, app.Paths.StateFile())
	be := &stateLockingBackend{Backend: testBackend(t, app), app: app}
	app.backendForTest = be
	defer func() {
		if be.held != nil {
			be.held.Release()
		}
	}()
	code, out = captureStdout(t, func() int { return runUseAuto(ctx, app, opts, "main", false) })
	if code == 0 || !strings.Contains(out, "recording state failed") || !strings.Contains(out, "live state restored") {
		t.Fatalf("failure must reach transaction rollback: %s", out)
	}
	if readFile(t, app.Env.Home+"/.codex/auth.json") != before || readFile(t, app.Paths.MiseGlobalFragmentFile()) != fragment || readFile(t, app.Paths.StateFile()) != beforeState {
		t.Fatal("mixed rollback changed previous selection or store")
	}
}
