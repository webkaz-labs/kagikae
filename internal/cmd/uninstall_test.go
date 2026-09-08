package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/integration"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/state"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
)

func TestUninstallPreviewDoesNotMutate(t *testing.T) {
	app := testApp(t, nil)
	path := uninstallCompletionPaths(app)["bash"][0]
	script, _ := completionScript("bash")
	writeFile(t, path, script)
	plan := app.planUninstall(commonOpts{DryRun: true}, nil, filepath.Join(app.Env.Home, "kae"))
	if len(plan.operations) != 1 || plan.operations[0].item.Action != constants.UninstallRemove {
		t.Fatalf("unexpected plan: %+v", plan.report)
	}
	if _, err := os.Stat(app.Paths.LocksDir()); !os.IsNotExist(err) {
		t.Fatalf("preview created locks: %v", err)
	}
	if got := readFile(t, path); got != script {
		t.Fatal("preview changed completion")
	}
}

func TestUninstallOwnedAndCustomCompletions(t *testing.T) {
	app := testApp(t, nil)
	zsh := uninstallCompletionPaths(app)["zsh"]
	script, _ := completionScript("zsh")
	for _, path := range zsh {
		writeFile(t, path, script)
	}
	custom := script + "\n# my customization\n"
	writeFile(t, zsh[1], custom)
	code, output := captureStdout(t, func() int {
		return runUninstall(context.Background(), app, commonOpts{Format: formatJSON, Yes: true}, nil, filepath.Join(app.Env.Home, "kae"))
	})
	mustExit(t, constants.ExitUnsafeRefused, code, output)
	var report uninstallReport
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatal(err)
	}
	if report.OK || report.Binary != constants.UninstallPending || report.Integrations != constants.UninstallIncomplete {
		t.Fatalf("incomplete removal reported complete: %s", output)
	}
	if got := readFile(t, zsh[1]); got != custom {
		t.Fatal("custom completion changed")
	}
	for _, path := range []string{zsh[0], zsh[2], zsh[3]} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("owned completion not removed: %s %v", path, err)
		}
	}
}

func TestUninstallRechecksContentAndWriterLock(t *testing.T) {
	app := testApp(t, nil)
	path := uninstallCompletionPaths(app)["bash"][0]
	script, _ := completionScript("bash")
	writeFile(t, path, script)
	plan := app.planUninstall(commonOpts{DryRun: true}, nil, filepath.Join(app.Env.Home, "kae"))
	writeFile(t, path, script+"\n# custom\n")
	if err := app.applyUninstall(plan.operations[0]); !errors.Is(err, integration.ErrChanged) {
		t.Fatalf("changed content accepted: %v", err)
	}
	writeFile(t, path, script)
	plan = app.planUninstall(commonOpts{DryRun: true}, nil, filepath.Join(app.Env.Home, "kae"))
	l, err := app.acquireNamedLock("completion", "busy")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	if code := exitOf(app.applyUninstall(plan.operations[0])); code != constants.ExitLockBusy {
		t.Fatalf("lock ignored: %d", code)
	}
	code, output := captureStdout(t, func() int { return runCompletionRefresh(app, commonOpts{Format: formatJSON}) })
	mustExit(t, constants.ExitLockBusy, code, output)
}

func TestUninstallBoundDirectoryRetainsStores(t *testing.T) {
	app := testApp(t, nil)
	dir := t.TempDir()
	entries := app.bondIsolationEntries([]runTarget{{Tool: constants.ToolClaude, Account: "side"}}, paths.PinID(dir))
	fragment := filepath.Join(dir, fragmentRelPath)
	writeFile(t, fragment, renderDirFragment("side", modeShared, entries, nil, nil))
	store := filepath.Join(app.Paths.CredStoreDir(constants.ToolClaude, "side"), "credentials")
	writeFile(t, store, "fixture-secret-must-survive")
	if err := app.recordPinnedDir(paths.PinID(dir), dir); err != nil {
		t.Fatal(err)
	}
	code, output := captureStdout(t, func() int {
		return runUninstall(context.Background(), app, commonOpts{Format: formatJSON, Yes: true}, []string{dir, dir}, filepath.Join(app.Env.Home, "kae"))
	})
	mustExit(t, constants.ExitUnsafeRefused, code, output) // no direct receipt
	if strings.Contains(output, "fixture-secret") {
		t.Fatal("credential content leaked")
	}
	if got := readFile(t, store); got != "fixture-secret-must-survive" {
		t.Fatal("retained credential changed")
	}
	if _, err := os.Stat(fragment); !os.IsNotExist(err) {
		t.Fatalf("fragment remains: %v", err)
	}
	if _, err := os.Stat(app.Paths.PinRecordFile(paths.PinID(dir))); err != nil {
		t.Fatalf("breadcrumb removed: %v", err)
	}
}

func TestUninstallGlobalRetainsCredentialsAndClearsSelection(t *testing.T) {
	app := testApp(t, nil)
	st := state.New()
	st.Synced = map[string]string{constants.ToolClaude: "side"}
	if err := os.MkdirAll(app.Paths.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		t.Fatal(err)
	}
	writeFile(t, app.Paths.MiseGlobalFragmentFile(), app.renderGlobalFragment(st.Synced)+miseHookBlock("bash"))
	code, output := captureStdout(t, func() int {
		return runUninstall(context.Background(), app, commonOpts{Format: formatJSON, Yes: true}, nil, filepath.Join(app.Env.Home, "kae"))
	})
	mustExit(t, constants.ExitUnsafeRefused, code, output)
	got, err := app.loadState()
	if err != nil || len(got.Synced) != 0 {
		t.Fatalf("selection not cleared: %v %v", got, err)
	}
	if _, err := os.Stat(app.Paths.MiseGlobalFragmentFile()); !os.IsNotExist(err) {
		t.Fatalf("fragment not removed: %v", err)
	}
}

func TestUninstallProjectOwnership(t *testing.T) {
	app := testApp(t, nil)
	block := app.miseBlock("side", true)
	for _, input := range []struct {
		name, content string
		owned         bool
	}{
		{"generated", block, true},
		{"unrelated", block + "\n[tools]\ngo = \"1.27.1\"\n", true},
		{"custom", strings.Replace(block, "kae use --auto --quiet", "kae use side", 1), false},
		{"marker_in_string", "note = '''\n" + block + "'''\n", false},
	} {
		t.Run(input.name, func(t *testing.T) {
			rest, owned := app.uninstallProjectRemainder(input.content)
			if owned != input.owned {
				t.Fatalf("ownership %t, want %t", owned, input.owned)
			}
			if input.name == "unrelated" && string(rest) != "\n[tools]\ngo = \"1.27.1\"\n" {
				t.Fatalf("unrelated config changed: %q", rest)
			}
		})
	}
}

func TestUninstallIncompleteGlobalState(t *testing.T) {
	for _, scenario := range []string{"migration", "invalid_state", "invalid_config"} {
		t.Run(scenario, func(t *testing.T) {
			app := testApp(t, nil)
			fragment := app.Paths.MiseGlobalFragmentFile()
			original := miseHookBlock("bash")
			writeFile(t, fragment, original)
			var preserved string
			switch scenario {
			case "migration":
				preserved = filepath.Join(app.Paths.StateDir, "mise-completion-migration.json")
			case "invalid_state":
				preserved = app.Paths.StateFile()
			case "invalid_config":
				app.ConfigErr = errors.New("invalid fixture config")
				preserved = app.ConfigPath
			}
			writeFile(t, preserved, "invalid fixture")
			for range 2 {
				code, output := captureStdout(t, func() int {
					return runUninstall(context.Background(), app, commonOpts{Format: formatJSON, Yes: true}, nil, filepath.Join(app.Env.Home, "kae"))
				})
				mustExit(t, constants.ExitUnsafeRefused, code, output)
				var report uninstallReport
				if err := json.Unmarshal([]byte(output), &report); err != nil {
					t.Fatal(err)
				}
				if report.OK || report.Integrations != constants.UninstallIncomplete || report.Binary != constants.UninstallPending {
					t.Fatalf("incomplete state hidden: %s", output)
				}
				if got := readFile(t, preserved); got != "invalid fixture" {
					t.Fatal("unresolved state changed")
				}
				if scenario != "invalid_config" && readFile(t, fragment) != original {
					t.Fatal("unresolved global integration changed")
				}
			}
		})
	}
}

func TestUninstallManagedGuidanceUsesExactRequest(t *testing.T) {
	app := testApp(t, nil)
	dir := filepath.Join(app.Env.Home, "project with ' quote")
	path := filepath.Join(dir, "mise.toml")
	writeFile(t, path, "[tools]\n\"packslip:github.com/webkaz-labs/kagikae\" = \"0.21.0\"\n")
	executable := filepath.Join(paths.XDGDataHome(app.Env.Getenv, app.Env.Home, "mise"), "installs", "kae", "0.21.0", "kae")
	fake := &runnertest.Fake{Stdout: "--path <PATH> --no-prune"}
	var guidance []string
	runner.With(fake, func() { guidance = app.managedUninstallGuidance(executable, []string{dir, dir}) })
	if fake.Name != "mise" || !reflect.DeepEqual(fake.Args, []string{"unuse", "--help"}) {
		t.Fatalf("unexpected manager invocation: %s %v", fake.Name, fake.Args)
	}
	if len(guidance) != 1 || !strings.Contains(guidance[0], "--path "+shellSingleQuote(path)+" 'packslip:github.com/webkaz-labs/kagikae'") {
		t.Fatalf("exact request not retained: %v", guidance)
	}
	if got := readFile(t, path); !strings.Contains(got, "0.21.0") {
		t.Fatal("manager config changed during discovery")
	}
}
