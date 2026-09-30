// Copilot adapter tests: config pointer, COPILOT_HOME, detection and broken config.

package adapter_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/adapter/copilot"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

const copilotConfigFixture = `// User settings belong in settings.json.
// This file is managed automatically.
{
  "trustedFolders": ["/workspaces"],
  "lastLoggedInUser": {"host":"https://github.com","login":"main"},
  "loggedInUsers": [{"host":"https://github.com","login":"main"}]
}
`

func TestCopilotArtifactsJSONCPointer(t *testing.T) {
	env := testEnv(t, "darwin", nil)
	specs, err := copilotAdapter.Artifacts(context.Background(), env)
	if err != nil || len(specs) != 1 {
		t.Fatalf("unexpected specs: %+v %v", specs, err)
	}
	if specs[0].Kind != constants.KindJSONPointer || specs[0].Pointer != "/lastLoggedInUser" || !specs[0].JSONC {
		t.Fatalf("expected a JSONC json-pointer spec: %+v", specs[0])
	}
	if specs[0].Target != filepath.Join(env.Home, ".copilot", "config.json") {
		t.Fatalf("unexpected target: %+v", specs[0])
	}
}

// COPILOT_HOME replaces ~/.copilot outright — it is the config directory itself,
// not a parent. Re-measured on 1.0.79, where it is also the mechanism copilot's own
// (deprecated) --config-dir flag points users at.
func TestCopilotHonorsCopilotHome(t *testing.T) {
	copilotHome := t.TempDir()
	env := testEnv(t, "darwin", map[string]string{copilot.EnvHome: copilotHome})
	specs, err := copilotAdapter.Artifacts(context.Background(), env)
	if err != nil || len(specs) != 1 {
		t.Fatalf("unexpected specs: %+v %v", specs, err)
	}
	if specs[0].Target != filepath.Join(copilotHome, "config.json") {
		t.Fatalf("COPILOT_HOME not honored: %+v", specs[0])
	}
	// The default location must not be consulted at all: a config.json there is
	// a different account's pointer, so reading it would report the wrong login.
	write(t, filepath.Join(env.Home, ".copilot", "config.json"), copilotConfigFixture)
	info, err := copilotAdapter.Detect(context.Background(), env)
	if err != nil || info.AuthPresent {
		t.Fatalf("$HOME/.copilot must be ignored while COPILOT_HOME is set: %+v %v", info, err)
	}
	write(t, filepath.Join(copilotHome, "config.json"), copilotConfigFixture)
	info, err = copilotAdapter.Detect(context.Background(), env)
	if err != nil || !info.AuthPresent {
		t.Fatalf("COPILOT_HOME config.json not detected: %+v %v", info, err)
	}
	if len(info.Warnings) != 0 {
		t.Fatalf("an absolute COPILOT_HOME must not warn: %+v", info.Warnings)
	}
}

// A relative COPILOT_HOME resolves against the *tool's* working directory, and
// kae is invoked from anywhere in a project — so the file kae writes is the file
// copilot reads only while both run from the same directory. kae keeps following
// the value (there is no default to fall back to while it is set) and warns,
// because the alternative is a silently wrong write with every guard green.
func TestCopilotWarnsOnRelativeCopilotHome(t *testing.T) {
	wantEnvConflictWarning(t, copilotAdapter,
		map[string]string{copilot.EnvHome: ".copilot-local"}, copilot.EnvHome+" is relative")
}

func TestCopilotDetect(t *testing.T) {
	env := testEnv(t, "linux", nil)
	info, err := copilotAdapter.Detect(context.Background(), env)
	if err != nil || info.AuthPresent {
		t.Fatalf("no config.json should mean no auth: %+v %v", info, err)
	}

	cfg := filepath.Join(env.Home, ".copilot", "config.json")
	write(t, cfg, copilotConfigFixture)
	info, err = copilotAdapter.Detect(context.Background(), env)
	if err != nil || !info.AuthPresent || info.Driver != constants.DriverCopilotConfigPointer {
		t.Fatalf("unexpected: %+v %v", info, err)
	}

	// env override is warned about.
	env = testEnv(t, "linux", map[string]string{"GH_TOKEN": "x"})
	write(t, filepath.Join(env.Home, ".copilot", "config.json"), copilotConfigFixture)
	info, _ = copilotAdapter.Detect(context.Background(), env)
	warned := false
	for _, w := range info.Warnings {
		if strings.Contains(w, "GH_TOKEN") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("expected GH_TOKEN warning: %+v", info.Warnings)
	}
}

func TestCopilotRefusesBrokenConfig(t *testing.T) {
	env := testEnv(t, "linux", nil)
	write(t, filepath.Join(env.Home, ".copilot", "config.json"), `// c`+"\n"+`{not json`)
	checks := copilotAdapter.Doctor(context.Background(), env)
	foundError := false
	for _, check := range checks {
		if check.Code == constants.CheckAuthPresent && check.Status == constants.StatusError {
			foundError = true
		}
	}
	if !foundError {
		t.Fatalf("doctor should flag the unparseable config: %+v", checks)
	}
}
