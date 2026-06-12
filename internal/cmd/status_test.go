package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/state"
)

func TestStatusShowsPinAndProfiles(t *testing.T) {
	app := testApp(t, nil)
	app.Config.Profiles = map[string]config.Profile{
		"personal": {Accounts: map[string]string{constants.ToolClaude: "kaz"}},
		"work":     {Accounts: map[string]string{constants.ToolClaude: "work", constants.ToolCodex: "work"}},
	}
	// Pinned-directory env: KAE_PROFILE plus an overlay-pointing config dir.
	app.Env.Getenv = func(key string) string {
		switch key {
		case constants.EnvKaeProfile:
			return "personal"
		case "CLAUDE_CONFIG_DIR":
			return app.Paths.OverlayDir(constants.ToolClaude, "kaz")
		}
		return ""
	}
	// Recorded global state: profile personal was applied.
	st := state.New()
	st.Active[constants.ToolClaude] = "kaz"
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
	if report.Pinned == nil || report.Pinned.Profile != "personal" || report.Pinned.Mode != "overlay" {
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
		"This directory: profile personal (pinned, overlay)",
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

func TestStatusRecordedProfileBeatsMappingMatch(t *testing.T) {
	app := testApp(t, nil)
	app.Config.Profiles = map[string]config.Profile{
		// Both profiles map the same single account; the recorded name wins.
		"a": {Accounts: map[string]string{constants.ToolClaude: "kaz"}},
		"b": {Accounts: map[string]string{constants.ToolClaude: "kaz"}},
	}
	st := state.New()
	st.Active[constants.ToolClaude] = "kaz"
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
}
