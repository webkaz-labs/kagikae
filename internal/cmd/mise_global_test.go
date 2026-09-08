package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/lock"
	"github.com/webkaz-labs/kagikae/internal/paths"
)

func TestGlobalMiseCompletionAndIsolationCoexist(t *testing.T) {
	for _, completionFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "completion-first", false: "isolation-first"}[completionFirst], func(t *testing.T) {
			app := applyTestApp(t, nil)
			ctx := context.Background()
			opts := commonOpts{Format: formatJSON}
			install := func() {
				t.Helper()
				if _, _, err := installMiseGlobalHook(app.Env, "zsh"); err != nil {
					t.Fatal(err)
				}
			}
			if completionFirst {
				install()
			}
			code, out := captureStdout(t, func() int { return runUseIsolated(ctx, app, opts, "claude", "main") })
			mustExit(t, 0, code, out)
			if !completionFirst {
				install()
			}
			before := readFile(t, app.Paths.MiseGlobalFragmentFile())
			if !strings.Contains(before, miseHookBlock("zsh")) || !strings.Contains(before, "CLAUDE_CONFIG_DIR") {
				t.Fatal("features did not coexist")
			}
			code, out = captureStdout(t, func() int { return runUseAuto(ctx, app, opts, "side", false) })
			mustExit(t, 0, code, out)
			if decodeBareUseReport(t, out).Changed || readFile(t, app.Paths.MiseGlobalFragmentFile()) != before {
				t.Fatal("auto rewrote combined fragment")
			}
			code, out = captureStdout(t, func() int { return runUseBare(ctx, app, opts, false, "side", false) })
			mustExit(t, 0, code, out)
			if got := readFile(t, app.Paths.MiseGlobalFragmentFile()); got != miseHookBlock("zsh") {
				t.Fatal("shared teardown removed completion or retained isolation")
			}
			if ok, err := app.globalFragmentConsistent(nil); err != nil || !ok {
				t.Fatalf("completion-only consistency: %v %v", ok, err)
			}
			install()
		})
	}
}

func TestGlobalMiseMigrationResumesAfterSourceRemoval(t *testing.T) {
	app := testApp(t, nil)
	source := globalMiseConfigPath(app.Env)
	dest := app.Paths.MiseGlobalFragmentFile()
	original := "[tools]\nnode = \"24\"\n"
	writeFile(t, source, original)
	journal := filepath.Join(app.Paths.StateDir, "mise-completion-migration.json")
	data, err := json.Marshal(miseCompletionMigration{Source: source, Destination: dest, Shell: "zsh"})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, journal, string(data))
	_, shell, registered, changed, err := refreshLegacyMiseGlobalHook(app.Env)
	if err != nil || shell != "zsh" || !registered || !changed {
		t.Fatalf("resume: %q %v %v %v", shell, registered, changed, err)
	}
	if readFile(t, source) != original || readFile(t, dest) != miseHookBlock("zsh") {
		t.Fatal("resume lost source or completion")
	}
	if _, err := os.Stat(journal); !os.IsNotExist(err) {
		t.Fatal("journal was not completed")
	}
}

func TestGlobalMiseMigrationWriteFailureRestoresSource(t *testing.T) {
	app := testApp(t, nil)
	source := globalMiseConfigPath(app.Env)
	original := miseHookBlock("zsh")
	writeFile(t, source, original)
	// Refuse the destination write after the migration intent and source removal.
	parent := filepath.Dir(app.Paths.MiseGlobalFragmentFile())
	if err := os.MkdirAll(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(parent, 0o700)
	if err := os.WriteFile(filepath.Join(parent, "probe"), nil, 0o600); err == nil {
		t.Skip("permissions not enforced")
	}
	_, _, err := installMiseGlobalHook(app.Env, "zsh")
	if err == nil {
		t.Fatal("destination failure passed")
	}
	if readFile(t, source) != original {
		t.Fatal("failed install changed source")
	}
	if _, err := os.Stat(filepath.Join(app.Paths.StateDir, "mise-completion-migration.json")); err != nil {
		t.Fatal("failure did not reach journaled migration")
	}
}

func TestGlobalMiseCompletionUsesStateLock(t *testing.T) {
	app := testApp(t, nil)
	l, err := lock.Acquire(app.Paths.LocksDir(), lockNameState)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	if _, _, err := installMiseGlobalHook(app.Env, "zsh"); exitOf(err) != constants.ExitLockBusy {
		t.Fatalf("expected lock busy, got %v", err)
	}
	if _, err := os.Stat(app.Paths.MiseGlobalFragmentFile()); !os.IsNotExist(err) {
		t.Fatal("contended install wrote fragment")
	}
}

func TestGlobalMiseCustomDirectoryAndForeignFragment(t *testing.T) {
	app := testApp(t, nil)
	base := app.Env.Getenv
	custom := filepath.Join(t.TempDir(), "mise")
	app.Env.Getenv = func(k string) string {
		if k == "MISE_CONFIG_DIR" {
			return custom
		}
		return base(k)
	}
	app.Paths = paths.Resolve(app.Env.Getenv, app.Env.Home)
	expected := filepath.Join(custom, "conf.d", "kagikae.toml")
	path, _, err := installMiseGlobalHook(app.Env, "fish")
	if err != nil || path != expected {
		t.Fatalf("custom root: %s %v", path, err)
	}
	original := "[hooks.enter]\nscript = \"echo private-sentinel\"\n"
	writeFile(t, path, original)
	_, _, err = installMiseGlobalHook(app.Env, "zsh")
	if err == nil || strings.Contains(err.Error(), "private-sentinel") {
		t.Fatalf("foreign refusal leaks or absent: %v", err)
	}
	if readFile(t, path) != original {
		t.Fatal("foreign fragment changed")
	}
}

func TestGlobalMiseAccountRenamePreservesCompletion(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatJSON}
	captureClaude(t, app, "main", mainToken)
	if _, _, err := installMiseGlobalHook(app.Env, "zsh"); err != nil {
		t.Fatal(err)
	}
	code, out := captureStdout(t, func() int { return runUseIsolated(ctx, app, opts, "claude", "main") })
	mustExit(t, 0, code, out)
	if _, err := buildAccountRename(ctx, app, opts, "claude", "main", "alt"); exitOf(err) != constants.ExitUnsafeRefused || !strings.Contains(err.Error(), "selected by global isolated mode") {
		t.Fatalf("isolated rename guard changed: %v", err)
	}
	code, out = captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") })
	mustExit(t, 0, code, out)
	if _, err := buildAccountRename(ctx, app, opts, "claude", "main", "alt"); err != nil {
		t.Fatal(err)
	}
	st, err := app.loadState()
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Synced) != 0 {
		t.Fatal("rename reintroduced isolation")
	}
	if ok, err := app.globalFragmentConsistent(st.Synced); err != nil || !ok {
		t.Fatalf("renamed combined fragment inconsistent: %v", err)
	}
	if !strings.Contains(readFile(t, app.Paths.MiseGlobalFragmentFile()), miseHookBlock("zsh")) {
		t.Fatal("rename lost completion")
	}
}

func TestGlobalMiseForeignContentRefusedBeforePreparation(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatJSON}
	captureClaude(t, app, "main", mainToken)
	original := "[hooks.enter]\nscript = \"echo private-sentinel\"\n"
	writeFile(t, app.Paths.MiseGlobalFragmentFile(), original)
	code, out := captureStdout(t, func() int { return runUseIsolated(ctx, app, opts, "claude", "main") })
	mustExit(t, constants.ExitUnsafeRefused, code, out)
	if _, err := os.Stat(app.Paths.GlobalIsolatedHomeDir("claude", "main")); !os.IsNotExist(err) {
		t.Fatal("refusal prepared isolated home")
	}
	if readFile(t, app.Paths.MiseGlobalFragmentFile()) != original {
		t.Fatal("foreign content changed")
	}
}

func TestGlobalMiseDoesNotMigrateMarkerInsideString(t *testing.T) {
	app := testApp(t, nil)
	block := miseHookBlock("zsh")
	original := "example = '''\n" + block + "'''\n[hooks.enter]\nshell = \"zsh\"\nscript = \"eval \\\"$(kae completion zsh)\\\"\"\n"
	source := globalMiseConfigPath(app.Env)
	writeFile(t, source, original)
	_, _, registered, changed, err := refreshLegacyMiseGlobalHook(app.Env)
	if err != nil || registered || changed {
		t.Fatalf("inert marker recognized: %v %v %v", registered, changed, err)
	}
	if readFile(t, source) != original {
		t.Fatal("string example changed")
	}
}

func TestGlobalMiseRecognizesPreviousIsolatedHeader(t *testing.T) {
	app := testApp(t, nil)
	selected := map[string]string{constants.ToolClaude: "main"}
	content := strings.Replace(app.renderGlobalFragment(selected), globalIsolatedOwnershipLine, legacyGlobalIsolatedOwnershipLine, 1)
	writeFile(t, app.Paths.MiseGlobalFragmentFile(), content)
	if ok, err := app.globalFragmentConsistent(selected); err != nil || !ok {
		t.Fatalf("previous isolated registration rejected: %v", err)
	}
	if readFile(t, app.Paths.MiseGlobalFragmentFile()) != content {
		t.Fatal("consistency check rewrote legacy content")
	}
}

func TestGlobalMiseIncompleteJournalRefusesWithoutWrites(t *testing.T) {
	app := testApp(t, nil)
	source := globalMiseConfigPath(app.Env)
	original := miseHookBlock("zsh")
	writeFile(t, source, original)
	writeFile(t, filepath.Join(app.Paths.StateDir, "mise-completion-migration.json"), "{}")
	_, _, _, _, err := refreshLegacyMiseGlobalHook(app.Env)
	if exitOf(err) != constants.ExitUnsafeRefused {
		t.Fatalf("invalid journal accepted: %v", err)
	}
	if readFile(t, source) != original {
		t.Fatal("invalid journal changed source")
	}
	if _, err := os.Stat(app.Paths.MiseGlobalFragmentFile()); !os.IsNotExist(err) {
		t.Fatal("invalid journal created destination")
	}
}
