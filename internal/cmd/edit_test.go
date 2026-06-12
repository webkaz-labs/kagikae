package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

func TestEditMissingConfigPointsAtInit(t *testing.T) {
	app := testApp(t, nil)
	code, out := captureStdout(t, func() int {
		return runEdit(context.Background(), app, commonOpts{Format: formatText})
	})
	mustExit(t, constants.ExitNotFound, code, out)
}

func TestEditLaunchesEditorAndValidates(t *testing.T) {
	app := testApp(t, map[string]string{"VISUAL": "myedit --wait"})
	writeFile(t, app.ConfigPath, "version = 1\n")

	var gotName string
	var gotArgs []string
	withInteractive(t, func(_ context.Context, _ []string, name string, args ...string) (int, error) {
		gotName, gotArgs = name, args
		return 0, nil
	})
	code, out := captureStdout(t, func() int {
		return runEdit(context.Background(), app, commonOpts{Format: formatText})
	})
	mustExit(t, constants.ExitOK, code, out)
	if gotName != "myedit" || len(gotArgs) != 2 || gotArgs[0] != "--wait" || gotArgs[1] != app.ConfigPath {
		t.Fatalf("editor invocation: %s %v", gotName, gotArgs)
	}
	if !strings.Contains(out, "Config OK") {
		t.Fatalf("expected validation confirmation: %s", out)
	}
}

func TestEditInvalidResultExitsInvalidConfig(t *testing.T) {
	app := testApp(t, nil) // no VISUAL/EDITOR -> vi fallback (mocked anyway)
	writeFile(t, app.ConfigPath, "version = 1\n")
	withInteractive(t, func(context.Context, []string, string, ...string) (int, error) {
		writeFile(t, app.ConfigPath, "version = 1\ndefault_profile = \"missing\"\n")
		return 0, nil
	})
	code, out := captureStdout(t, func() int {
		return runEdit(context.Background(), app, commonOpts{Format: formatText})
	})
	mustExit(t, constants.ExitInvalidConfig, code, out)
}
