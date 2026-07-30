package cmd

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
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

// allBinariesOnPath makes every tool's binary resolvable, so the probe round
// covers all of them concurrently.
func allBinariesOnPath(app *App) {
	app.Env.LookPath = func(name string) (string, error) { return "/usr/local/bin/" + name, nil }
}

// slowFake is a concurrency-safe runner.Runner (the probes now run in parallel,
// so runnertest.Fake's unguarded fields would race) that sleeps per binary before
// answering, and records the order in which it was called.
type slowFake struct {
	delay  map[string]time.Duration
	stdout string

	mu    sync.Mutex
	calls []string
}

func (f *slowFake) Run(ctx context.Context, name string, _ ...string) (string, string, int) {
	f.mu.Lock()
	f.calls = append(f.calls, name)
	f.mu.Unlock()
	select {
	case <-time.After(f.delay[name]):
	case <-ctx.Done():
		return "", "killed", 1 // what exec.CommandContext yields on a deadline
	}
	return f.stdout, "", 0
}

func (f *slowFake) RunInput(ctx context.Context, _ string, name string, args ...string) (string, string, int) {
	return f.Run(ctx, name, args...)
}

// The probes run concurrently, but the findings must stay in canonical tool
// order: the report is compared by eye and by test across runs, so a
// completion-order result would reshuffle doctor's output at random. The delays
// are deliberately inverse to the tool order, so an implementation that appended
// as goroutines finished would emit exactly the reverse.
//
// cursor is absent from the expectation on purpose: it declares no verified
// version (date-versioned), so it is skipped even though its binary resolves.
func TestUpstreamVersionChecksStayInToolOrder(t *testing.T) {
	app := testApp(t, nil)
	allBinariesOnPath(app)
	fake := &slowFake{
		stdout: "999.0.0\n", // past every adapter's verified version
		delay: map[string]time.Duration{
			"claude": 30 * time.Millisecond, "codex": 25 * time.Millisecond,
			"agy": 20 * time.Millisecond, "opencode": 15 * time.Millisecond,
			"cursor-agent": 10 * time.Millisecond, "copilot": 5 * time.Millisecond,
		},
	}
	var got []adapter.Check
	start := time.Now()
	runner.With(fake, func() { got = app.upstreamVersionChecks(context.Background(), "") })
	elapsed := time.Since(start)

	want := []string{
		constants.ToolClaude, constants.ToolCodex, constants.ToolAgy,
		constants.ToolOpencode, constants.ToolCopilot,
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d checks (cursor declares no version), got %+v", len(want), got)
	}
	for i, tool := range want {
		if got[i].Tool != tool {
			t.Errorf("check %d is %s, want %s (findings must follow constants.Tools)", i, got[i].Tool, tool)
		}
	}
	// Serially this is 105ms of sleeping; concurrently it is bounded by the
	// slowest probe (30ms). The margin is wide enough not to flake on a busy box.
	if elapsed > 80*time.Millisecond {
		t.Errorf("probes took %v; they must run concurrently, not one after another", elapsed)
	}
	// cursor is not merely filtered out of the findings: declaring no version
	// means its probe is never spawned at all.
	if len(fake.calls) != len(want) {
		t.Errorf("expected one probe per version-declaring tool, got %v", fake.calls)
	}
	for _, name := range fake.calls {
		if name == "cursor-agent" {
			t.Errorf("cursor declares no verified version; probing it is wasted work: %v", fake.calls)
		}
	}
}

// A binary that never answers must not hang doctor: the probe round carries its
// own deadline, and a killed probe is the same silent skip as any other failing
// `--version`. The fake outlives the deadline by 25x, so without one this test
// would report findings instead of none.
func TestUpstreamVersionChecksHonorDeadline(t *testing.T) {
	saved := upstreamVersionProbeDeadline
	upstreamVersionProbeDeadline = 20 * time.Millisecond
	defer func() { upstreamVersionProbeDeadline = saved }()

	app := testApp(t, nil)
	allBinariesOnPath(app)
	delay := map[string]time.Duration{}
	for _, tool := range constants.Tools {
		ad, err := adapter.ForTool(tool)
		if err != nil {
			t.Fatal(err)
		}
		delay[ad.Binary()] = 500 * time.Millisecond
	}
	fake := &slowFake{stdout: "999.0.0\n", delay: delay}

	var got []adapter.Check
	start := time.Now()
	runner.With(fake, func() { got = app.upstreamVersionChecks(context.Background(), "") })
	if len(got) != 0 {
		t.Fatalf("a probe killed by the deadline must be skipped, got %+v", got)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("the round took %v; the deadline must cut off a wedged probe", elapsed)
	}
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

// TestAssumptionAgeChecks covers the blind spot upstream_version cannot: it only
// fires when the installed tool moves past the verified release, so a user who
// never upgrades — or who uses cursor, which declares no usable version — gets
// no signal that an assumption set has gone unexamined.
func TestAssumptionAgeChecks(t *testing.T) {
	app := testApp(t, nil)
	verified, err := time.Parse(time.DateOnly, mustAdapter(t, constants.ToolClaude).VerifiedOn())
	if err != nil {
		t.Fatal(err)
	}

	// One day short of the threshold: silent.
	app.Now = func() time.Time { return verified.Add(assumptionMaxAge - 24*time.Hour) }
	for _, c := range app.assumptionAgeChecks(constants.ToolClaude) {
		t.Fatalf("a fresh assumption set must not warn: %q", c.Message)
	}

	// One day past it: one warning, naming the date and the doc to work.
	app.Now = func() time.Time { return verified.Add(assumptionMaxAge + 24*time.Hour) }
	checks := app.assumptionAgeChecks(constants.ToolClaude)
	if len(checks) != 1 {
		t.Fatalf("expected one stale-assumption check, got %+v", checks)
	}
	if checks[0].Code != constants.CheckUpstreamVersion || checks[0].Status != constants.StatusWarn {
		t.Fatalf("unexpected check shape: %+v", checks[0])
	}
	for _, want := range []string{mustAdapter(t, constants.ToolClaude).VerifiedOn(), "VALIDATION.md", "181 days ago"} {
		if !strings.Contains(checks[0].Message, want) {
			t.Errorf("message must contain %q: %q", want, checks[0].Message)
		}
	}
}

func mustAdapter(t *testing.T, tool string) adapter.Adapter {
	t.Helper()
	ad, err := adapter.ForTool(tool)
	if err != nil {
		t.Fatal(err)
	}
	return ad
}
