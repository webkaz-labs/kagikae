package cmd

import (
	"context"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/companion"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

func TestDoctorCompanionChecks(t *testing.T) {
	app := companionCLIApp(t)
	// Bind git (inline) and a gh token marker with no stored secret, to exercise
	// both the binary-missing and missing-token checks. testApp's LookPath always
	// fails, so every bound companion's CLI counts as absent.
	app.Config.Profiles["main"] = config.Profile{
		Accounts: map[string]string{constants.ToolClaude: "main"},
		Companions: map[string]config.CompanionData{
			constants.CompanionGit: {"email": "you@example.com"},
			constants.CompanionGH:  {"GH_TOKEN": ""},
		},
	}
	report := buildDoctor(context.Background(), app, "", false)
	if _, ok := findCheck(report, constants.CheckCompanionMissing); !ok {
		t.Error("expected companion_missing for an unstored gh token")
	}
	if _, ok := findCheck(report, constants.CheckCompanionBinary); !ok {
		t.Error("expected companion_binary when a bound companion CLI is missing from PATH")
	}
}

func TestDoctorCompanionTokenStoredNoMissing(t *testing.T) {
	app := companionCLIApp(t)
	app.Config.Profiles["main"] = config.Profile{
		Accounts: map[string]string{constants.ToolClaude: "main"},
		// expected_login is inline metadata, never in the secret backend; it must
		// not be probed as a missing token (regression guard).
		Companions: map[string]config.CompanionData{constants.CompanionGH: {"GH_TOKEN": "", "expected_login": "octocat"}},
	}
	be, err := app.secretBackend()
	if err != nil {
		t.Fatal(err)
	}
	if err := be.Set(context.Background(), companion.SecretRef("main", "gh", "GH_TOKEN"), []byte("ghp_x")); err != nil {
		t.Fatal(err)
	}
	report := buildDoctor(context.Background(), app, "", false)
	if _, ok := findCheck(report, constants.CheckCompanionMissing); ok {
		t.Error("a stored token plus expected_login metadata must not raise companion_missing")
	}
}

func TestDoctorToolFilterSkipsCompanionChecks(t *testing.T) {
	app := companionCLIApp(t)
	app.Config.Profiles["main"] = config.Profile{
		Accounts:   map[string]string{constants.ToolClaude: "main"},
		Companions: map[string]config.CompanionData{constants.CompanionGH: {"GH_TOKEN": ""}},
	}
	// A tool-filtered report is about one tool; companions are not tools.
	report := buildDoctor(context.Background(), app, constants.ToolClaude, false)
	if _, ok := findCheck(report, constants.CheckCompanionMissing); ok {
		t.Error("tool-filtered doctor must not run companion checks")
	}
}
