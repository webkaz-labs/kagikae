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

// TestStatusDetectsConcurrently proves the per-tool Detect runs concurrently
// (docs/RELEASE.md §A acceptance). Every adapter's Detect calls env.LookPath as
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
		"personal": {Accounts: map[string]string{constants.ToolClaude: "work"}},
		"work":     {Accounts: map[string]string{constants.ToolClaude: "work", constants.ToolCodex: "work"}},
	}
	// Pinned-directory env: KAE_PROFILE plus an isolated-bind config dir.
	app.Env.Getenv = func(key string) string {
		switch key {
		case constants.EnvKaeProfile:
			return "personal"
		case "CLAUDE_CONFIG_DIR":
			return app.Paths.IsolatedConfigDir("abcdef0123456789", constants.ToolClaude, "work")
		}
		return ""
	}
	// Recorded global state: profile personal was applied.
	st := state.New()
	st.Active[constants.ToolClaude] = "work"
	st.ActiveProfile = "personal"
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
	if report.Pinned == nil || report.Pinned.Profile != "personal" || report.Pinned.Mode != "isolated" {
		t.Fatalf("pinned context missing: %+v", report.Pinned)
	}
	if report.ActiveProfile == nil || *report.ActiveProfile != "personal" {
		t.Fatalf("active profile missing: %s", out)
	}
	if len(report.Profiles) != 2 || report.Profiles[0].Name != "personal" || !report.Profiles[0].Active || report.Profiles[1].Active {
		t.Fatalf("profiles listing wrong: %s", out)
	}

	code, out = captureStdout(t, func() int {
		return runStatus(context.Background(), app, commonOpts{Format: formatText})
	})
	mustExit(t, constants.ExitOK, code, out)
	for _, want := range []string{
		"This directory: profile personal (pinned, isolated)",
		"Global active profile: personal",
		"Profiles:",
		"claude:work codex:work",
		"(active)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status text missing %q: %s", want, out)
		}
	}
}

// TestStatusShowsActiveAccountIdentity: status surfaces the active account's
// recorded login identity (§D / v0.8.7), in both --json and text.
func TestStatusShowsActiveAccountIdentity(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	opts := commonOpts{Format: formatText}

	// seedClaude writes ~/.claude.json with emailAddress = <uuid>@example.com,
	// which claude.Identity detects and capture records into the snapshot.
	seedClaude(t, app, workToken, "work-uuid")
	if code, out := captureStdout(t, func() int { return runCapture(ctx, app, opts, "claude", "work") }); code != constants.ExitOK {
		t.Fatalf("capture: %s", out)
	}
	if code, out := captureStdout(t, func() int { return runSwitch(ctx, app, opts, "claude", "work") }); code != constants.ExitOK {
		t.Fatalf("switch: %s", out)
	}

	code, out := captureStdout(t, func() int { return runStatus(ctx, app, commonOpts{Format: formatJSON}) })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, `"identity": "work-uuid@example.com"`) {
		t.Fatalf("status --json must carry the active account's identity: %s", out)
	}
	code, out = captureStdout(t, func() int { return runStatus(ctx, app, commonOpts{Format: formatText}) })
	mustExit(t, constants.ExitOK, code, out)
	if !strings.Contains(out, "work-uuid@example.com") {
		t.Fatalf("status text must show the identity column: %s", out)
	}
}

func TestStatusRecordedProfileBeatsMappingMatch(t *testing.T) {
	app := testApp(t, nil)
	app.Config.Profiles = map[string]config.Profile{
		// Both profiles map the same single account; the recorded name wins.
		"a": {Accounts: map[string]string{constants.ToolClaude: "work"}},
		"b": {Accounts: map[string]string{constants.ToolClaude: "work"}},
	}
	st := state.New()
	st.Active[constants.ToolClaude] = "work"
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
