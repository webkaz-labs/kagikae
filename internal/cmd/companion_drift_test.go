package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"text/template"

	"github.com/webkaz-labs/kagikae/internal/companion"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/runner"
)

// gitConfigFake answers `git config --get user.<field>` from a per-key map so a
// multi-knob drift test can give email and name different live values. A key
// absent from the map is reported unset (exit 1), as real git does.
type gitConfigFake struct{ values map[string]string }

func (f gitConfigFake) Run(_ context.Context, name string, args ...string) (string, string, int) {
	if name == "git" && len(args) == 3 && args[0] == "config" && args[1] == "--get" {
		if v, ok := f.values[args[2]]; ok {
			return v + "\n", "", 0
		}
		return "", "", 1
	}
	return "", "", 0
}

func (f gitConfigFake) RunInput(ctx context.Context, _ string, name string, args ...string) (string, string, int) {
	return f.Run(ctx, name, args...)
}

// driftApp builds a doctor app pinned (via a written fragment) to a profile that
// binds the git companion to the given identity fields, with git on PATH.
func driftApp(t *testing.T, gitData config.CompanionData) *App {
	t.Helper()
	app := companionCLIApp(t)
	app.Config.Profiles["main"] = config.Profile{
		Accounts:   map[string]string{constants.ToolClaude: "main"},
		Companions: map[string]config.CompanionData{constants.CompanionGit: gitData},
	}
	// git must look present, else the probe skips (companion_binary covers absence).
	app.Env.LookPath = func(string) (string, error) { return "/usr/bin/git", nil }
	chdirTemp(t)
	writeFile(t, fragmentRelPath, "# kae:profile=main\n")
	return app
}

func TestDoctorGitDriftDetected(t *testing.T) {
	app := driftApp(t, config.CompanionData{"email": "you@example.com", "name": "main"})
	fake := gitConfigFake{values: map[string]string{
		"user.email": "side@example.com", // drift
		"user.name":  "main",             // matches
	}}
	var report *doctorReport
	runner.With(fake, func() { report = buildDoctor(context.Background(), app, "", false) })

	msg, ok := findCheck(report, constants.CheckCompanionDrift)
	if !ok {
		t.Fatal("expected companion_drift when live git email differs from the binding")
	}
	if !strings.Contains(msg, "user.email") || !strings.Contains(msg, "you@example.com") {
		t.Errorf("drift message should name the field and bound value: %q", msg)
	}
	// The matching name knob must not also drift.
	for _, c := range report.Checks {
		if c.Code == constants.CheckCompanionDrift && strings.Contains(c.Message, "user.name") {
			t.Errorf("matching knob must not drift: %q", c.Message)
		}
	}
}

func TestDoctorGitDriftUnset(t *testing.T) {
	app := driftApp(t, config.CompanionData{"email": "you@example.com"})
	// git config returns nothing (unset / pin not active in this shell).
	fake := gitConfigFake{values: map[string]string{}}
	var report *doctorReport
	runner.With(fake, func() { report = buildDoctor(context.Background(), app, "", false) })

	msg, ok := findCheck(report, constants.CheckCompanionDrift)
	if !ok {
		t.Fatal("expected companion_drift when live git email is unset")
	}
	if !strings.Contains(msg, "not active in this shell") {
		t.Errorf("unset message should point at the inactive pin: %q", msg)
	}
}

func TestDoctorGitDriftEmptyValue(t *testing.T) {
	app := driftApp(t, config.CompanionData{"email": "you@example.com"})
	// git config --get succeeds (exit 0) but the key is set to an empty value:
	// distinct from unset, and still a drift against the non-empty binding.
	fake := gitConfigFake{values: map[string]string{"user.email": ""}}
	var report *doctorReport
	runner.With(fake, func() { report = buildDoctor(context.Background(), app, "", false) })

	msg, ok := findCheck(report, constants.CheckCompanionDrift)
	if !ok {
		t.Fatal("expected companion_drift when live git email is set to empty")
	}
	if strings.Contains(msg, "not active in this shell") {
		t.Errorf("an explicitly-empty value (exit 0) must not read as unset: %q", msg)
	}
	if !strings.Contains(msg, "you@example.com") {
		t.Errorf("drift message should name the bound value: %q", msg)
	}
}

func TestDoctorGitNoDriftWhenMatches(t *testing.T) {
	app := driftApp(t, config.CompanionData{"email": "you@example.com", "name": "main"})
	fake := gitConfigFake{values: map[string]string{
		"user.email": "you@example.com",
		"user.name":  "main",
	}}
	var report *doctorReport
	runner.With(fake, func() { report = buildDoctor(context.Background(), app, "", false) })

	if _, ok := findCheck(report, constants.CheckCompanionDrift); ok {
		t.Error("no drift when live git identity matches the binding")
	}
}

func TestDoctorGitDriftSkippedWhenNotPinned(t *testing.T) {
	app := companionCLIApp(t)
	app.Config.Profiles["main"] = config.Profile{
		Accounts:   map[string]string{constants.ToolClaude: "main"},
		Companions: map[string]config.CompanionData{constants.CompanionGit: {"email": "you@example.com"}},
	}
	app.Env.LookPath = func(string) (string, error) { return "/usr/bin/git", nil }
	chdirTemp(t) // no fragment written: the directory is not pinned
	fake := gitConfigFake{values: map[string]string{"user.email": "side@example.com"}}
	var report *doctorReport
	runner.With(fake, func() { report = buildDoctor(context.Background(), app, "", false) })

	if _, ok := findCheck(report, constants.CheckCompanionDrift); ok {
		t.Error("drift must not run outside a pinned directory")
	}
}

func TestDoctorGitDriftSkippedWhenGitMissing(t *testing.T) {
	app := driftApp(t, config.CompanionData{"email": "you@example.com"})
	app.Env.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	fake := gitConfigFake{values: map[string]string{"user.email": "side@example.com"}}
	var report *doctorReport
	runner.With(fake, func() { report = buildDoctor(context.Background(), app, "", false) })

	if _, ok := findCheck(report, constants.CheckCompanionDrift); ok {
		t.Error("drift probe must skip when git is absent (companion_binary covers it)")
	}
}

func TestDoctorGitDriftToolFilterSkips(t *testing.T) {
	app := driftApp(t, config.CompanionData{"email": "you@example.com"})
	fake := gitConfigFake{values: map[string]string{"user.email": "side@example.com"}}
	var report *doctorReport
	// A tool-filtered report is about one tool; companions are not tools.
	runner.With(fake, func() { report = buildDoctor(context.Background(), app, constants.ToolClaude, false) })

	if _, ok := findCheck(report, constants.CheckCompanionDrift); ok {
		t.Error("tool-filtered doctor must not run companion drift checks")
	}
}

// TestGitCompanionTemplateMatchesDriftKeys guards the coupling companionDrift
// relies on: each git knob X is git config key user.X. If the template ever
// renders a knob under a different section, this fails so the drift probe is
// corrected with it (mirrors the config/companion knob-name drift guard).
func TestGitCompanionTemplateMatchesDriftKeys(t *testing.T) {
	spec, ok := companion.For(constants.CompanionGit)
	if !ok {
		t.Fatal("git companion not registered")
	}
	data := map[string]string{"HomeGitconfig": ""}
	for _, k := range spec.Knobs {
		data[k.Name] = "probe-" + k.Name
	}
	tmpl, err := template.New("git").Parse(spec.FileTmpl)
	if err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatal(err)
	}
	// Walk the rendered INI and record which knobs appear *inside* the [user]
	// section. A knob under any other section would make user.<knob> the wrong
	// drift key, so containment alone is not enough.
	rendered := buf.String()
	underUser := map[string]bool{}
	inUser := false
	for _, line := range strings.Split(rendered, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inUser = trimmed == "[user]"
			continue
		}
		if inUser {
			for _, k := range spec.Knobs {
				if strings.HasPrefix(trimmed, k.Name+" = ") {
					underUser[k.Name] = true
				}
			}
		}
	}
	for _, k := range spec.Knobs {
		if !underUser[k.Name] {
			t.Errorf("git knob %q must render under [user] (so user.%s is the drift key); template:\n%s", k.Name, k.Name, rendered)
		}
	}
}
