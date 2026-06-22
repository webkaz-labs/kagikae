package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// companionTestApp builds an app whose "main" profile binds one of each
// override kind: git (git-config data), gh (token marker), kubectl (config-dir
// path). A pre-existing ~/.gitconfig lets the include-preservation be checked.
func companionTestApp(t *testing.T) *App {
	t.Helper()
	app := testApp(t, nil)
	app.Config.Profiles = map[string]config.Profile{
		"main": {Accounts: map[string]string{constants.ToolClaude: "main"}, Companions: map[string]config.CompanionData{
			constants.CompanionGit:     {"email": "you@example.com", "name": "You", "signingkey": ""},
			constants.CompanionGH:      {"GH_TOKEN": ""},
			constants.CompanionKubectl: {"KUBECONFIG": "/home/me/.kube/config"},
		}},
	}
	writeFile(t, filepath.Join(app.Env.Home, ".gitconfig"), "[alias]\n\tlol = log --oneline\n")
	return app
}

func TestCompanionPlanEntriesAndRedactions(t *testing.T) {
	app := companionTestApp(t)
	entries, redactions, prepare, err := app.companionPlan("main")
	if err != nil {
		t.Fatalf("companionPlan: %v", err)
	}
	if err := prepare(); err != nil {
		t.Fatalf("prepare: %v", err)
	}

	byVar := map[string]companionEnvEntry{}
	for _, e := range entries {
		byVar[e.EnvVar] = e
	}

	// git-config: GIT_CONFIG_GLOBAL points at the generated file, not a secret.
	git, ok := byVar["GIT_CONFIG_GLOBAL"]
	if !ok || git.Lookup != nil {
		t.Fatalf("git entry = %+v, want literal path", git)
	}
	if git.Value != app.Paths.CompanionConfigFile("main", constants.CompanionGit) {
		t.Fatalf("git path = %q", git.Value)
	}

	// config-dir: KUBECONFIG is the user-supplied path, set directly.
	kube, ok := byVar["KUBECONFIG"]
	if !ok || kube.Lookup != nil || kube.Value != "/home/me/.kube/config" {
		t.Fatalf("kubectl entry = %+v", kube)
	}

	// token: GH_TOKEN is a lookup, never a literal value.
	gh, ok := byVar["GH_TOKEN"]
	if !ok || gh.Lookup == nil || gh.Value != "" {
		t.Fatalf("gh entry = %+v, want lookup", gh)
	}
	if gh.Lookup[1] != companionTokenSubcmd || gh.Lookup[2] != "main" ||
		gh.Lookup[3] != constants.CompanionGH || gh.Lookup[4] != "GH_TOKEN" {
		t.Fatalf("gh lookup argv = %v", gh.Lookup)
	}

	// Only the token knob is redacted.
	if len(redactions) != 1 || redactions[0] != "GH_TOKEN" {
		t.Fatalf("redactions = %v, want [GH_TOKEN]", redactions)
	}
}

func TestCompanionPlanWritesIncludingGitconfig(t *testing.T) {
	app := companionTestApp(t)
	_, _, prepare, err := app.companionPlan("main")
	if err != nil {
		t.Fatalf("companionPlan: %v", err)
	}
	if err := prepare(); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	got := readFile(t, app.Paths.CompanionConfigFile("main", constants.CompanionGit))
	homeGitconfig := filepath.Join(app.Env.Home, ".gitconfig")
	if !strings.Contains(got, "path = "+homeGitconfig) {
		t.Errorf("generated config must [include] the home gitconfig:\n%s", got)
	}
	if !strings.Contains(got, "email = you@example.com") || !strings.Contains(got, "name = You") {
		t.Errorf("generated config missing identity override:\n%s", got)
	}
	// Empty signingkey is omitted, not emitted as a blank line.
	if strings.Contains(got, "signingkey") {
		t.Errorf("empty signingkey must be omitted:\n%s", got)
	}
	// The real ~/.gitconfig is never modified.
	if home := readFile(t, homeGitconfig); strings.Contains(home, "you@example.com") {
		t.Errorf("~/.gitconfig must not be rewritten:\n%s", home)
	}
}

func TestCompanionFragmentLinesNeverLeakSecret(t *testing.T) {
	app := companionTestApp(t)
	entries, redactions, _, err := app.companionPlan("main")
	if err != nil {
		t.Fatalf("companionPlan: %v", err)
	}
	frag := strings.Join(companionFragmentLines(entries), "\n")
	exports := strings.Join(companionExportLines(entries), "\n")

	// The token reaches neither rendering as a literal: only an exec()/$() that
	// invokes the helper at eval time.
	if !strings.Contains(frag, `GH_TOKEN = "{{ exec(command=`) {
		t.Errorf("fragment must deliver GH_TOKEN via exec():\n%s", frag)
	}
	if !strings.Contains(frag, companionTokenSubcmd) {
		t.Errorf("fragment exec must call the token helper:\n%s", frag)
	}
	if !strings.Contains(exports, `export GH_TOKEN="$(`) {
		t.Errorf("export fallback must resolve GH_TOKEN via $():\n%s", exports)
	}
	// Literal knobs render directly.
	if !strings.Contains(frag, `KUBECONFIG = "/home/me/.kube/config"`) {
		t.Errorf("fragment must set KUBECONFIG path:\n%s", frag)
	}
	if len(redactions) == 0 {
		t.Error("token companion must contribute a redaction")
	}
}

func TestCompanionPlanNoProfileCompanionsIsNoop(t *testing.T) {
	app := testApp(t, nil)
	app.Config.Profiles = map[string]config.Profile{"main": {Accounts: map[string]string{constants.ToolClaude: "main"}}}
	entries, redactions, prepare, err := app.companionPlan("main")
	if err != nil {
		t.Fatalf("companionPlan: %v", err)
	}
	if entries != nil || redactions != nil {
		t.Fatalf("expected no entries/redactions, got %v / %v", entries, redactions)
	}
	if err := prepare(); err != nil {
		t.Fatalf("noop prepare: %v", err)
	}
}
