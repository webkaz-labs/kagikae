package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
)

// deadClaudeCred is a claude credential past every deadline: access token expired
// in 2020, refresh token in 2021. Named because six tests age a store's credential
// to exactly this state, and six copies of one payload is six chances for one to
// drift into meaning something else.
const deadClaudeCred = `{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":1577836800000,"refreshTokenExpiresAt":1609459200000}}`

// endOfLifeClaudeCred is the oauth object of a claude credential with **no refresh
// token**, so its access-token expiry is the whole deadline — cursor's shape, and the
// simplest fixture for exercising the lead-time band without a second timestamp in
// play. (A refresh-backed credential is judged on its login deadline the same way;
// TestRefreshBackedLoginDeadlineInTheBandWarns covers that side.) Six tests across
// five files build this with different tokens and horizons, which is one shape in six
// places — the same reason deadClaudeCred exists.
func endOfLifeClaudeCred(now time.Time, in time.Duration, token string) string {
	return fmt.Sprintf(`{"accessToken":%q,"refreshToken":"","expiresAt":%d}`,
		token, now.Add(in).UnixMilli())
}

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
		deadClaudeCred)

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

	writeFile(t, credFile, `{"claudeAiOauth":`+endOfLifeClaudeCred(app.Now(), 5*24*time.Hour, "a")+`}`)

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

// A healthy bound credential says nothing.
func TestHealthyBoundDirectoryCredentialIsSilent(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	pinWithCapturedClaude(t, app)

	if checks := app.pinCredentialChecks(ctx); len(checks) != 0 {
		t.Fatalf("a healthy bound credential must be silent, got %+v", checks)
	}
}

// `kae unpin` keeps the store on purpose so a re-pin restores its sessions — but
// nothing in that directory points at it any more. Reporting its credential would
// claim "bound to" about a directory that is not bound, and send the user to a
// login that lands somewhere the tool will no longer read. pinChecks skips an
// unpinned directory for the same reason.
//
// The credential is aged *after* unpinning here on purpose: with a healthy one this
// test passes whether or not the gate exists, which is exactly how the gate came to
// be missing in the first place.
func TestUnpinnedDirectoryCredentialIsNotReported(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	_, credFile := pinWithCapturedClaude(t, app)

	code, out := captureStdout(t, func() int { return runUnpin(ctx, app, commonOpts{Format: formatText}, false) })
	mustExit(t, constants.ExitOK, code, out)
	writeFile(t, credFile,
		deadClaudeCred)

	if checks := app.pinCredentialChecks(ctx); len(checks) != 0 {
		t.Fatalf("an unpinned directory's kept store must stay silent even when stale, got %+v", checks)
	}
}

// The same rule one level down: re-binding a single tool leaves the previously
// bound tools' stores on disk, and those are no longer what the directory points
// at. Only what the fragment binds *now* may be reported, so the sweep reads the
// fragment rather than walking the store tree.
func TestUnboundToolsStoreInABoundDirectoryIsNotReported(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	dir, credFile := pinWithCapturedClaude(t, app)

	// Re-bind this directory to codex alone; claude's store stays on disk.
	seedCodex(t, app, "codex-main")
	if code, out := captureStdout(t, func() int {
		return runCapture(ctx, app, commonOpts{Format: formatText}, "codex", "main")
	}); code != constants.ExitOK {
		t.Fatalf("capture codex/main: %s", out)
	}
	app.Config.Profiles = map[string]config.Profile{
		"main": {Accounts: map[string]string{constants.ToolCodex: "main"}},
	}
	if code, out := captureStdout(t, func() int {
		return runPin(ctx, app, commonOpts{Format: formatText}, "main", modeShared)
	}); code != constants.ExitOK {
		t.Fatalf("re-pin to codex only: %s", out)
	}

	// claude's leftover store dies; the directory no longer binds claude.
	writeFile(t, credFile,
		deadClaudeCred)

	for _, c := range app.pinCredentialChecks(ctx) {
		if c.Tool == constants.ToolClaude {
			t.Fatalf("%s no longer binds claude; its leftover store must not be reported: %q", dir, c.Message)
		}
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
		deadClaudeCred)

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
		deadClaudeCred)

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

// boundStoreDir branches on the fragment's mode, and a two-branch switch with one
// branch untested is the shape of a guard that checks nothing. Isolated mode puts
// the store at a per-account path instead of the shared one, so a wrong branch
// reads a directory that does not exist and the check goes quietly silent — the
// failure that looks exactly like "no problem found".
func TestDoctorReportsStaleCredentialInAnIsolatedBoundDirectory(t *testing.T) {
	app := overlayTestApp(t)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaudeOAuth(t, app,
		`{"accessToken":"a","refreshToken":"r","expiresAt":1577836800000,"refreshTokenExpiresAt":4000000000000}`)
	if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") }); code != constants.ExitOK {
		t.Fatalf("capture claude/main: %s", out)
	}
	dir := pinHere(t, app, modeIsolated)

	credFile := filepath.Join(
		app.Paths.IsolatedConfigDir(paths.PinID(dir), constants.ToolClaude, "main"), ".credentials.json",
	)
	if readFile(t, credFile) == "" {
		t.Fatalf("an isolated pin must materialize a credential at %s", credFile)
	}
	writeFile(t, credFile,
		deadClaudeCred)

	msg, ok := findCheck(buildDoctor(ctx, app, "", false), constants.CheckCredentialStale)
	if !ok {
		t.Fatal("an isolated bound directory's dead credential must be reported too")
	}
	if !strings.Contains(msg, "bound to "+dir) {
		t.Fatalf("message must name the bound directory: %q", msg)
	}
}
