package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
)

// pinWithCapturedClaude captures a healthy claude/main, binds a temp directory to
// it, and returns the bound directory plus the path of the credential copy that
// directory now owns. The tool refreshes *that* copy in place, which is why it can
// go stale while the account snapshot stays fine.
func pinWithCapturedClaude(t *testing.T, app *App) (dir, credFile string) {
	t.Helper()
	ctx := context.Background()
	opts := commonOpts{Format: formatText}
	// Deadline in 2096: healthy, so nothing is reported until a test ages it.
	seedClaudeOAuth(t, app,
		`{"accessToken":"a","refreshToken":"r","expiresAt":1577836800000,"refreshTokenExpiresAt":4000000000000}`)
	if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") }); code != constants.ExitOK {
		t.Fatalf("capture claude/main: %s", out)
	}
	dir = pinHere(t, app, modeShared)
	credFile = filepath.Join(app.Paths.SharedDir(paths.PinID(dir), constants.ToolClaude), ".credentials.json")
	if readFile(t, credFile) == "" {
		t.Fatalf("pin did not materialize a credential at %s", credFile)
	}
	return dir, credFile
}

// A bound directory holds its own copy of the credential and the tool refreshes
// that copy in place, so it can die while every account snapshot kae has still
// looks fine. credential_stale reads snapshots, so nothing reported this at all —
// the first signal was the tool refusing to start in that directory.
func TestDoctorReportsStaleBoundDirectoryCredential(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	dir, credFile := pinWithCapturedClaude(t, app)

	// The directory's copy died; the account snapshot is untouched and healthy.
	writeFile(t, credFile,
		`{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1577836800000,"refreshTokenExpiresAt":1609459200000}}`)

	report := buildDoctor(ctx, app, "", false)
	if report.SchemaVersion != constants.SchemaVersion {
		t.Fatalf("schema_version changed: %d", report.SchemaVersion)
	}
	msg, ok := findCheck(report, constants.CheckCredentialStale)
	if !ok {
		t.Fatalf("expected a credential_stale check for the bound directory, got %+v", report.Checks)
	}
	for _, want := range []string{"bound to " + dir, "refresh token expired", "cd " + dir, "claude /login"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q: %q", want, msg)
		}
	}
	// The remedy must be the login *inside* the directory, never a re-bind: the
	// account snapshot it would re-copy can be just as dead as this copy.
	if strings.Contains(msg, "kae pin") || strings.Contains(msg, "kae add") {
		t.Errorf("a bound-directory credential is not fixed by re-binding or re-capturing: %q", msg)
	}
	// Warn-level, so a dead bound credential never turns `kae doctor` into a
	// non-zero exit — which would break the mise enter hook.
	for _, c := range report.Checks {
		if c.Code == constants.CheckCredentialStale && c.Status != constants.StatusWarn {
			t.Fatalf("credential_stale must be warn-level, got %q", c.Status)
		}
	}
	// The healthy account snapshot must not be dragged in with it.
	for _, c := range report.Checks {
		if c.Code == constants.CheckCredentialStale && strings.Contains(c.Message, "snapshot \"main\"") {
			t.Errorf("the account snapshot is healthy and must not be reported: %q", c.Message)
		}
	}
}

// The lead-time half, in a bound directory: still working, deadline days away.
func TestDoctorReportsExpiringBoundDirectoryCredential(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	dir, credFile := pinWithCapturedClaude(t, app)

	writeFile(t, credFile, `{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1577836800000,`+
		`"refreshTokenExpiresAt":`+strconv.FormatInt(app.Now().Add(5*24*time.Hour).UnixMilli(), 10)+`}}`)

	report := buildDoctor(ctx, app, "", false)
	msg, ok := findCheck(report, constants.CheckCredentialExpiring)
	if !ok {
		t.Fatalf("expected a credential_expiring check for the bound directory, got %+v", report.Checks)
	}
	for _, want := range []string{"bound to " + dir, "5 day(s)", "cd " + dir} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q: %q", want, msg)
		}
	}
	if _, stale := findCheck(report, constants.CheckCredentialStale); stale {
		t.Error("a credential that still works must not also be reported stale")
	}
}

// The negatives, all of which used to be the whole behaviour: a healthy bound
// credential says nothing, and a directory whose binding is already broken is
// pinChecks' finding, not reported twice.
func TestBoundDirectoryCredentialNegatives(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	pinWithCapturedClaude(t, app)

	if checks := app.pinCredentialChecks(ctx); len(checks) != 0 {
		t.Fatalf("a healthy bound credential must be silent, got %+v", checks)
	}

	// An unpinned directory keeps its store on purpose; its credential is not a
	// finding, exactly as pinChecks treats the binding.
	code, out := captureStdout(t, func() int { return runUnpin(ctx, app, commonOpts{Format: formatText}, false) })
	mustExit(t, constants.ExitOK, code, out)
	if checks := app.pinCredentialChecks(ctx); len(checks) != 0 {
		t.Fatalf("an unpinned directory's kept store must stay silent, got %+v", checks)
	}
}

// A directory that no longer exists is pinChecks' orphaned-store finding. Naming
// it again here would report one problem twice, and the credential of a store
// nothing can reach is moot.
func TestDeletedBoundDirectoryCredentialIsNotReportedTwice(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	dir, credFile := pinWithCapturedClaude(t, app)
	writeFile(t, credFile,
		`{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1577836800000,"refreshTokenExpiresAt":1609459200000}}`)

	chdirTemp(t) // step out before removing it
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if checks := app.pinCredentialChecks(ctx); len(checks) != 0 {
		t.Fatalf("a deleted directory is pinChecks' finding only, got %+v", checks)
	}
	if _, ok := findCheck(buildDoctor(ctx, app, "", false), constants.CheckPinStale); !ok {
		t.Fatal("pinChecks must still report the orphaned store")
	}
}

// The bound-directory sweep is not per-tool, so `kae doctor <tool>` skips it —
// same rule as pinChecks and the companion checks, and doctor says so on stderr.
func TestFilteredDoctorSkipsBoundDirectoryCredentials(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	_, credFile := pinWithCapturedClaude(t, app)
	writeFile(t, credFile,
		`{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1577836800000,"refreshTokenExpiresAt":1609459200000}}`)

	for _, c := range buildDoctor(ctx, app, constants.ToolClaude, false).Checks {
		if strings.Contains(c.Message, "bound to ") {
			t.Fatalf("a filtered run must not include the bound-directory sweep: %q", c.Message)
		}
	}
}

// A new output path built from a live credential payload. AGENTS.md requires a
// redaction test for each one, and this is the only one that reads a *live* store
// rather than a snapshot, so it is where a raw payload is closest to the message.
func TestBoundDirectoryCredentialMessageNeverCarriesTheToken(t *testing.T) {
	const canary = "sk-ant-oat01-PIN-CANARY-iiii"
	app := overlayTestApp(t)
	ctx := context.Background()
	_, credFile := pinWithCapturedClaude(t, app)

	for _, payload := range []string{
		// stale, expiring, and the tombstone, which is the branch that names no dates
		// and so has the least structure holding it back from echoing the payload.
		`{"claudeAiOauth":{"accessToken":"` + canary + `","refreshToken":"` + canary +
			`-r","expiresAt":1577836800000,"refreshTokenExpiresAt":1609459200000}}`,
		`{"claudeAiOauth":{"accessToken":"` + canary + `","refreshToken":"` + canary +
			`-r","expiresAt":1577836800000,"refreshTokenExpiresAt":` +
			strconv.FormatInt(app.Now().Add(2*24*time.Hour).UnixMilli(), 10) + `}}`,
		`{"claudeAiOauth":{"accessToken":"","refreshToken":"","expiresAt":0,"note":"` + canary + `"}}`,
	} {
		writeFile(t, credFile, payload)
		for _, format := range []string{formatText, formatJSON} {
			_, stdout, stderr := captureBoth(t, func() int {
				return runDoctor(ctx, app, commonOpts{Format: format}, "")
			})
			if strings.Contains(stdout+stderr, canary) {
				t.Fatalf("doctor (%s) leaked a bound credential value:\n%s\n%s", format, stdout, stderr)
			}
		}
	}
}
