package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
)

// TestParseUpstreamVersion pins the six real `--version` outputs (measured
// 2026-07-29) against the one extractor, so a shape nobody anticipated — a
// trailing period, a date version, a bare number — cannot silently start
// reporting the wrong version or none at all.
func TestParseUpstreamVersion(t *testing.T) {
	cases := []struct{ tool, output, want string }{
		{constants.ToolClaude, "2.1.220 (Claude Code)\n", "2.1.220"},
		{constants.ToolCodex, "codex-cli 0.145.0\n", "0.145.0"},
		{constants.ToolAgy, "1.0.10\n", "1.0.10"},
		{constants.ToolOpencode, "1.17.4\n", "1.17.4"},
		{constants.ToolCursor, "2026.06.16-20-30-07-a07d3ac\n", "2026.06.16"},
		{constants.ToolCopilot, "GitHub Copilot CLI 1.0.61.\n", "1.0.61"},
	}
	for _, tc := range cases {
		raw, _, ok := parseUpstreamVersion(tc.output)
		if !ok || raw != tc.want {
			t.Errorf("%s: parseUpstreamVersion(%q) = %q, %v; want %q", tc.tool, tc.output, raw, ok, tc.want)
		}
	}
}

func TestParseUpstreamVersionRejectsNonVersion(t *testing.T) {
	for _, output := range []string{"", "no version here", "1.2", "v1", "2026-06-16"} {
		if _, _, ok := parseUpstreamVersion(output); ok {
			t.Errorf("parseUpstreamVersion(%q) must not report a version", output)
		}
	}
}

// Only a major/minor move warns: patch releases are weekly on claude, so warning
// on them would be noise, and an older installed version was covered by the
// verification that happened above it.
func TestUpstreamVersionCheckThresholds(t *testing.T) {
	const verified = "2.1.220"
	cases := []struct {
		name, output string
		want         bool
	}{
		{"same version", "2.1.220 (Claude Code)", false},
		{"patch newer", "2.1.221 (Claude Code)", false},
		{"patch older", "2.1.219 (Claude Code)", false},
		{"minor newer", "2.2.0 (Claude Code)", true},
		{"minor older", "2.0.999 (Claude Code)", false},
		{"major newer", "3.0.0 (Claude Code)", true},
		{"major older", "1.9.9 (Claude Code)", false},
		{"unparseable", "claude (unknown build)", false},
	}
	for _, tc := range cases {
		check, got := upstreamVersionCheck(constants.ToolClaude, tc.output, verified)
		if got != tc.want {
			t.Errorf("%s: upstreamVersionCheck(%q) warned = %v, want %v", tc.name, tc.output, got, tc.want)
			continue
		}
		if !got {
			continue
		}
		if check.Code != constants.CheckUpstreamVersion || check.Status != constants.StatusWarn ||
			check.Tool != constants.ToolClaude {
			t.Errorf("%s: unexpected check shape: %+v", tc.name, check)
		}
		for _, want := range []string{verified, "VALIDATION.md"} {
			if !strings.Contains(check.Message, want) {
				t.Errorf("%s: message should name %q: %q", tc.name, want, check.Message)
			}
		}
	}
}

// An unparseable declared version is a skip, not a warning: kae must never invent
// a finding out of its own bad data.
func TestUpstreamVersionCheckSkipsUnparseableVerified(t *testing.T) {
	if _, ok := upstreamVersionCheck(constants.ToolClaude, "9.9.9", "unknown"); ok {
		t.Error("an unparseable verified version must not produce a check")
	}
}

// claudeOnlyPath makes only claude's binary resolvable, so the probe loop touches
// exactly one tool (the others are skipped as not installed).
func claudeOnlyPath(app *App) {
	app.Env.LookPath = func(name string) (string, error) {
		if name == "claude" {
			return "/usr/local/bin/claude", nil
		}
		return "", errors.New("not found")
	}
}

func TestUpstreamVersionChecksProbesInstalledBinary(t *testing.T) {
	app := testApp(t, nil)
	claudeOnlyPath(app)
	fake := &runnertest.Fake{Stdout: "2.9.0 (Claude Code)\n"}
	runner.With(fake, func() {
		got := app.upstreamVersionChecks(context.Background(), "")
		if len(got) != 1 || got[0].Code != constants.CheckUpstreamVersion || got[0].Tool != constants.ToolClaude {
			t.Fatalf("expected one claude upstream_version check, got %+v", got)
		}
	})
	if fake.Name != "claude" || len(fake.Args) != 1 || fake.Args[0] != "--version" {
		t.Errorf("probe should be `claude --version`, got %s %v", fake.Name, fake.Args)
	}
}

func TestUpstreamVersionChecksSkipsFailingProbe(t *testing.T) {
	app := testApp(t, nil)
	claudeOnlyPath(app)
	fake := &runnertest.Fake{Stderr: "unknown flag: --version", Code: 1}
	runner.With(fake, func() {
		if got := app.upstreamVersionChecks(context.Background(), ""); len(got) != 0 {
			t.Fatalf("a failing --version must be skipped, got %+v", got)
		}
	})
}

// A tool that is not installed is binary_present's finding, not this check's.
func TestUpstreamVersionChecksSkipsMissingBinary(t *testing.T) {
	app := testApp(t, nil) // testApp's LookPath always fails
	fake := &runnertest.Fake{Stdout: "9.9.9\n"}
	runner.With(fake, func() {
		if got := app.upstreamVersionChecks(context.Background(), ""); len(got) != 0 {
			t.Fatalf("no installed binary must mean no check, got %+v", got)
		}
	})
	if fake.Name != "" {
		t.Errorf("must not probe a binary that is not on PATH, ran %q", fake.Name)
	}
}

// Both new codes are additive members of the doctor JSON contract, at
// schema_version 1.
func TestDoctorJSONCarriesUpstreamAndIdentityDrift(t *testing.T) {
	app := identityDriftApp(t)
	claudeOnlyPath(app)
	claudeJSON(t, app, `{"oauthAccount":{"emailAddress":"side@example.com"}}`)
	fake := &runnertest.Fake{Stdout: "2.9.0 (Claude Code)\n"}

	var out string
	runner.With(fake, func() {
		_, out = captureStdout(t, func() int {
			return runDoctor(context.Background(), app, commonOpts{Format: formatJSON}, constants.ToolClaude)
		})
	})
	for _, want := range []string{`"identity_drift"`, `"upstream_version"`, `"schema_version": 1`} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor --json should contain %s: %s", want, out)
		}
	}
	if strings.Contains(out, "side@example.com") {
		t.Errorf("identity payload must never reach --json: %s", out)
	}
}
