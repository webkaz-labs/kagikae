package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/paths"
)

// setupBondHome seeds a realistic claude real home in app.Env.Home.
func setupBondHome(t *testing.T, app *App) {
	t.Helper()
	home := filepath.Join(app.Env.Home, ".claude")
	writeFile(t, filepath.Join(home, ".credentials.json"), `{"token":"real"}`)
	writeFile(t, filepath.Join(home, "settings.json"), `{"theme":"dark"}`)
	writeFile(t, filepath.Join(home, "CLAUDE.md"), "# project\n")
}

func TestPrepareBondSymlinksNonDenylist(t *testing.T) {
	app := testApp(t, nil)
	setupBondHome(t, app)
	cwd := t.TempDir()
	pinID := paths.PinID(cwd)

	bondDir, err := app.prepareBond(constants.ToolClaude, "work", pinID)
	if err != nil {
		t.Fatalf("prepareBond: %v", err)
	}

	// settings.json and CLAUDE.md must be symlinks pointing into the real home.
	for _, item := range []string{"settings.json", "CLAUDE.md"} {
		dst := filepath.Join(bondDir, item)
		info, err := os.Lstat(dst)
		if err != nil {
			t.Fatalf("%s missing in bond dir: %v", item, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s must be a symlink in bond dir", item)
		}
		target, _ := os.Readlink(dst)
		want := filepath.Join(app.Env.Home, ".claude", item)
		if target != want {
			t.Errorf("%s symlink points to %q, want %q", item, target, want)
		}
	}
}

func TestPrepareBondCredentialIsPrivateCopy(t *testing.T) {
	app := testApp(t, nil)
	setupBondHome(t, app)
	cwd := t.TempDir()
	pinID := paths.PinID(cwd)

	bondDir, err := app.prepareBond(constants.ToolClaude, "work", pinID)
	if err != nil {
		t.Fatalf("prepareBond: %v", err)
	}

	dst := filepath.Join(bondDir, ".credentials.json")
	info, err := os.Lstat(dst)
	if err != nil {
		t.Fatalf(".credentials.json missing in bond dir: %v", err)
	}
	// Must be a regular file, not a symlink.
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error(".credentials.json must be a private copy, not a symlink")
	}
	if got := readFile(t, dst); !strings.Contains(got, "real") {
		t.Errorf(".credentials.json private copy has wrong content: %q", got)
	}
}

func TestPrepareBondIdempotent(t *testing.T) {
	app := testApp(t, nil)
	setupBondHome(t, app)
	cwd := t.TempDir()
	pinID := paths.PinID(cwd)

	// First run.
	if _, err := app.prepareBond(constants.ToolClaude, "work", pinID); err != nil {
		t.Fatalf("first prepareBond: %v", err)
	}
	// Second run must succeed without error.
	bondDir, err := app.prepareBond(constants.ToolClaude, "work", pinID)
	if err != nil {
		t.Fatalf("second prepareBond (idempotent): %v", err)
	}

	// Verify symlinks still correct after second run.
	dst := filepath.Join(bondDir, "settings.json")
	info, _ := os.Lstat(dst)
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("settings.json must remain a symlink after re-bond")
	}
}

func TestPrepareBondSkipsCredentialWhenNotLoggedIn(t *testing.T) {
	app := testApp(t, nil)
	// Real home exists but has no .credentials.json (not yet logged in).
	home := filepath.Join(app.Env.Home, ".claude")
	writeFile(t, filepath.Join(home, "settings.json"), `{}`)
	cwd := t.TempDir()
	pinID := paths.PinID(cwd)

	bondDir, err := app.prepareBond(constants.ToolClaude, "work", pinID)
	if err != nil {
		t.Fatalf("prepareBond: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bondDir, ".credentials.json")); !os.IsNotExist(err) {
		t.Error(".credentials.json must not exist in bond dir when tool is not logged in")
	}
}
