package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/state"
)

// TestStatusDetectsConcurrently proves the per-tool Detect runs concurrently.
// Every adapter's Detect calls env.LookPath as
// its binary probe; a LookPath that blocks until all enabled tools have entered
// can only be satisfied if the Detects overlap — a sequential loop would block
// on the first tool and never reach the second.
func TestStatusDetectsConcurrently(t *testing.T) {
	app := testApp(t, nil)
	n := len(app.enabledTools())
	if n < 2 {
		t.Skipf("need >=2 enabled tools to prove concurrency, got %d", n)
	}

	release := make(chan struct{})
	arrived := make(chan struct{}, n)
	app.Env.LookPath = func(string) (string, error) {
		arrived <- struct{}{}
		<-release
		return "", errors.New("not found")
	}

	// Silence the status JSON; restore stdout once the run completes.
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	oldStdout := os.Stdout
	os.Stdout = devnull
	defer func() { os.Stdout = oldStdout }()

	done := make(chan int, 1)
	go func() {
		done <- runStatus(context.Background(), app, commonOpts{Format: formatJSON})
	}()

	timeout := time.After(5 * time.Second)
	for i := range n {
		select {
		case <-arrived:
		case <-timeout:
			close(release) // unblock the run goroutine before failing
			t.Fatalf("Detect did not run concurrently: only %d of %d tools reached LookPath", i, n)
		}
	}
	close(release)
	if code := <-done; code != constants.ExitOK {
		t.Fatalf("status exited %d", code)
	}
}

func TestStatusShowsPinAndProfiles(t *testing.T) {
	app := testApp(t, nil)
	app.Config.Profiles = map[string]config.Profile{
		"side": {Accounts: map[string]string{constants.ToolClaude: "main"}},
		"main": {Accounts: map[string]string{constants.ToolClaude: "main", constants.ToolCodex: "main"}},
	}
	// Pinned-directory env: KAE_PROFILE plus an isolated-bind config dir.
	app.Env.Getenv = func(key string) string {
		switch key {
		case constants.EnvKaeProfile:
			return "side"
		case "CLAUDE_CONFIG_DIR":
			return app.Paths.IsolatedConfigDir("abcdef0123456789", constants.ToolClaude, "main")
		}
		return ""
	}
	// Recorded global state: profile side was applied.
	st := state.New()
	st.Active[constants.ToolClaude] = "main"
	st.ActiveProfile = "side"
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		t.Fatal(err)
	}

	code, out := captureStdout(t, func() int {
		return runStatus(context.Background(), app, commonOpts{Format: formatJSON})
	})
	mustExit(t, constants.ExitOK, code, out)
	var report statusReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid status JSON: %v: %s", err, out)
	}
	if report.Pinned == nil || report.Pinned.Profile != "side" || report.Pinned.Mode != "isolated" {
		t.Fatalf("pinned context missing: %+v", report.Pinned)
	}
	if report.ActiveProfile == nil || *report.ActiveProfile != "side" {
		t.Fatalf("active profile missing: %s", out)
	}
	if len(report.Profiles) != 2 || report.Profiles[0].Name != "main" || report.Profiles[0].Active || !report.Profiles[1].Active {
		t.Fatalf("profiles listing wrong: %s", out)
	}

	code, out = captureStdout(t, func() int {
		return runStatus(context.Background(), app, commonOpts{Format: formatText})
	})
	mustExit(t, constants.ExitOK, code, out)
	for _, want := range []string{
		"This directory: profile side (pinned, isolated)",
		"Global active profile: side",
		"Profiles:",
		"claude:main codex:main",
		"(active)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status text missing %q: %s", want, out)
		}
	}
}

// TestStatusShowsActiveAccountIdentity: status surfaces the active account's
// recorded login identity, in both --json and text.
func TestStatusShowsActiveAccountIdentity(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	// seedClaude writes ~/.claude.json with emailAddress = <uuid>@example.com,
	// which claude.Identity detects and capture records into the snapshot.
	seedClaude(t, app, mainToken, "main-uuid")
	if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "main") }); code != constants.ExitOK {
		t.Fatalf("capture: %s", out)
	}
	if code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "main") }); code != constants.ExitOK {
		t.Fatalf("switch: %s", out)
	}

	code, out := captureStdout(t, func() int { return runStatus(ctx, app, commonOpts{Format: formatJSON}) })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, `"identity": "main-uuid@example.com"`) {
		t.Fatalf("status --json must carry the active account's identity: %s", out)
	}
	code, out = captureStdout(t, func() int { return runStatus(ctx, app, commonOpts{Format: formatText}) })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, "main-uuid@example.com") {
		t.Fatalf("status text must show the identity column: %s", out)
	}
}

func TestStatusRecordedProfileBeatsMappingMatch(t *testing.T) {
	app := testApp(t, nil)
	app.Config.Profiles = map[string]config.Profile{
		// Both profiles map the same single account; the recorded name wins.
		"a": {Accounts: map[string]string{constants.ToolClaude: "main"}},
		"b": {Accounts: map[string]string{constants.ToolClaude: "main"}},
	}
	st := state.New()
	st.Active[constants.ToolClaude] = "main"
	st.ActiveProfile = "b"
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		t.Fatal(err)
	}
	code, out := captureStdout(t, func() int {
		return runStatus(context.Background(), app, commonOpts{Format: formatJSON})
	})
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, `"active_profile": "b"`) {
		t.Fatalf("recorded active_profile must win: %s", out)
	}

	// Without a recorded profile (older state files) the mapping match is
	// the fallback; with ambiguous mappings it resolves to the first name
	// in ascending order.
	st.ActiveProfile = ""
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		t.Fatal(err)
	}
	code, out = captureStdout(t, func() int {
		return runStatus(context.Background(), app, commonOpts{Format: formatJSON})
	})
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, `"active_profile": "a"`) {
		t.Fatalf("mapping-match fallback must resolve: %s", out)
	}
}

func TestOrDashRendersEmptyAsDash(t *testing.T) {
	if got := orDash(""); got != "-" {
		t.Fatalf(`orDash("") = %q, want "-"`, got)
	}
	if got := orDash("you@example.com"); got != "you@example.com" {
		t.Fatalf("orDash passthrough = %q", got)
	}
}

// `kae status` answers "what is my current setup", so the freshness it shows is
// the *active* account's. It is a different question from the existing Auth
// column, which reports the live store: a tool can be logged in right now while
// the snapshot kae would re-apply is already dead.
func TestStatusReportsActiveAccountCredentialFreshness(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	seedClaudeOAuth(t, app, endOfLifeClaudeCred(app.Now(), 2*24*time.Hour, "a"))
	captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "soon") })
	captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "soon") })

	_, out := captureStdout(t, func() int { return runStatus(ctx, app, commonOpts{Format: formatJSON}) })
	var report statusReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid status JSON: %v: %s", err, out)
	}
	if report.SchemaVersion != constants.SchemaVersion {
		t.Fatalf("schema_version = %d, want %d", report.SchemaVersion, constants.SchemaVersion)
	}
	var found bool
	for _, ts := range report.Tools {
		if ts.Tool != constants.ToolClaude {
			// A tool with no active account claims nothing.
			if ts.Credential != "" || ts.ReloginBy != "" {
				t.Errorf("%s has no active account but reported %q/%q", ts.Tool, ts.Credential, ts.ReloginBy)
			}
			continue
		}
		found = true
		if ts.Credential != constants.CredentialExpiring {
			t.Errorf("claude credential = %q, want %q", ts.Credential, constants.CredentialExpiring)
		}
		if ts.ReloginBy == "" {
			t.Error("a judged tool row must publish its deadline")
		}
	}
	if !found {
		t.Fatalf("claude row missing: %s", out)
	}
	_, text := captureStdout(t, func() int { return runStatus(ctx, app, commonOpts{Format: formatText, NoColor: true}) })
	if !strings.Contains(text, "Credential") || !strings.Contains(text, "2 day(s) left") {
		t.Fatalf("status table lost the credential column:\n%s", text)
	}
}
