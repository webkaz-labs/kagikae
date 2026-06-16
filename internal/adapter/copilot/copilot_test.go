package copilot

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/adapter"
)

func testEnv(home string) adapter.Env {
	return adapter.Env{
		GOOS:     "linux",
		Home:     home,
		Getenv:   func(string) string { return "" },
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
	}
}

func writeConfig(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".copilot")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Identity must read /lastLoggedInUser.login through the JSONC leading comments.
func TestCopilotIdentityFromLogin(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `// User settings belong in settings.json.
// This file is managed automatically.
{
  "trustedFolders": ["/workspaces"],
  "lastLoggedInUser": {"host": "https://github.com", "login": "octocat"},
}`)
	got, err := Copilot{}.Identity(t.Context(), testEnv(home))
	if err != nil || got != "octocat" {
		t.Fatalf("Identity = %q, err = %v; want octocat", got, err)
	}
}

func TestCopilotIdentityMissing(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `{"trustedFolders": []}`)
	var c Copilot
	if _, err := c.Identity(t.Context(), testEnv(home)); err == nil {
		t.Fatal("expected an error when /lastLoggedInUser is absent")
	}
}
