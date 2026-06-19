package cmd

import (
	"context"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

func reloadConfig(t *testing.T, app *App) *config.Config {
	t.Helper()
	cfg, _, err := config.Load(app.ConfigPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	return cfg
}

func TestProfileSaveFromActive(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	captureClaude(t, app, "main", mainToken) // claude=main active
	writeConfigFile(t, app, "version = 1\n# my config\n")

	if _, err := buildProfileSave(ctx, app, commonOpts{Format: formatText}, "dev"); err != nil {
		t.Fatalf("save: %v", err)
	}
	cfg := reloadConfig(t, app)
	if cfg.Profiles["dev"].Accounts["claude"] != "main" {
		t.Fatalf("save did not capture active: %+v", cfg.Profiles["dev"])
	}
}

func TestProfileSaveNoActiveFails(t *testing.T) {
	app := testApp(t, nil)
	writeConfigFile(t, app, "version = 1\n")
	if _, err := buildProfileSave(context.Background(), app, commonOpts{Format: formatText}, "dev"); exitOf(err) != constants.ExitNotFound {
		t.Fatalf("expected exit %d, got %v", constants.ExitNotFound, err)
	}
}

func TestProfileSetRequiresCapturedAccount(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	writeConfigFile(t, app, "version = 1\n")
	// Unknown account is rejected.
	if _, err := buildProfileSet(ctx, app, commonOpts{Format: formatText}, "dev", "claude", "ghost"); exitOf(err) != constants.ExitNotFound {
		t.Fatalf("expected exit %d for unknown account, got %v", constants.ExitNotFound, err)
	}
	// Captured account succeeds and creates the profile.
	captureClaude(t, app, "main", mainToken)
	writeConfigFile(t, app, "version = 1\n")
	if _, err := buildProfileSet(ctx, app, commonOpts{Format: formatText}, "dev", "claude", "main"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if reloadConfig(t, app).Profiles["dev"].Accounts["claude"] != "main" {
		t.Fatal("set did not create the mapping")
	}
}

func TestProfileUnsetDropsMappingAndEmptyProfile(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	writeConfigFile(t, app, "version = 1\n[profiles.dev.accounts]\nclaude = \"main\"\ncodex = \"main\"\n")

	if _, err := buildProfileUnset(ctx, app, commonOpts{Format: formatText}, "dev", "codex"); err != nil {
		t.Fatalf("unset: %v", err)
	}
	cfg := reloadConfig(t, app)
	if _, ok := cfg.Profiles["dev"].Accounts["codex"]; ok {
		t.Fatal("codex mapping not dropped")
	}
	if cfg.Profiles["dev"].Accounts["claude"] != "main" {
		t.Fatal("claude mapping lost")
	}

	// Unsetting the last mapping removes the now-empty profile.
	writeConfigFile(t, app, "version = 1\n[profiles.solo.accounts]\nclaude = \"main\"\n")
	if _, err := buildProfileUnset(ctx, app, commonOpts{Format: formatText}, "solo", "claude"); err != nil {
		t.Fatalf("unset last: %v", err)
	}
	if _, ok := reloadConfig(t, app).Profiles["solo"]; ok {
		t.Fatal("empty profile not removed")
	}
}

func TestProfileUnsetLastMappingOfDefaultClearsDefault(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	// The default profile has a single mapping; unsetting it removes the
	// profile and must clear default_profile, or the reload would reject the
	// dangling reference and leave config.toml invalid.
	writeConfigFile(t, app, "version = 1\ndefault_profile = \"dev\"\n[profiles.dev.accounts]\nclaude = \"main\"\n")
	if _, err := buildProfileUnset(ctx, app, commonOpts{Format: formatText}, "dev", "claude"); err != nil {
		t.Fatalf("unset last of default: %v", err)
	}
	cfg := reloadConfig(t, app)
	if _, ok := cfg.Profiles["dev"]; ok {
		t.Fatal("default profile not removed")
	}
	if cfg.DefaultProfile != "" {
		t.Fatalf("default_profile not cleared: %q", cfg.DefaultProfile)
	}
}

func TestProfileRmGuardsDefault(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	writeConfigFile(t, app, "version = 1\ndefault_profile = \"dev\"\n[profiles.dev.accounts]\nclaude = \"main\"\n")

	// Unknown profile exits not_found.
	if _, err := buildProfileRm(ctx, app, commonOpts{Format: formatText}, "ghost", false); exitOf(err) != constants.ExitNotFound {
		t.Fatalf("expected exit %d, got %v", constants.ExitNotFound, err)
	}
	// Removing the default without --force refuses.
	if _, err := buildProfileRm(ctx, app, commonOpts{Format: formatText}, "dev", false); exitOf(err) != constants.ExitUnsafeRefused {
		t.Fatalf("expected exit %d, got %v", constants.ExitUnsafeRefused, err)
	}
	// --force removes it and clears the default.
	if _, err := buildProfileRm(ctx, app, commonOpts{Format: formatText}, "dev", true); err != nil {
		t.Fatalf("rm --force: %v", err)
	}
	cfg := reloadConfig(t, app)
	if _, ok := cfg.Profiles["dev"]; ok {
		t.Fatal("profile not removed")
	}
	if cfg.DefaultProfile != "" {
		t.Fatalf("default not cleared: %q", cfg.DefaultProfile)
	}
}

func TestProfileDefaultSetClearAndUnknown(t *testing.T) {
	app := testApp(t, nil)
	ctx := context.Background()
	writeConfigFile(t, app, "version = 1\n[profiles.dev.accounts]\nclaude = \"main\"\n")

	// Unknown profile rejected.
	if _, err := buildProfileDefault(ctx, app, commonOpts{Format: formatText}, "ghost", false); exitOf(err) != constants.ExitNotFound {
		t.Fatalf("expected exit %d, got %v", constants.ExitNotFound, err)
	}
	// Set default.
	if _, err := buildProfileDefault(ctx, app, commonOpts{Format: formatText}, "dev", false); err != nil {
		t.Fatalf("default set: %v", err)
	}
	if reloadConfig(t, app).DefaultProfile != "dev" {
		t.Fatal("default not set")
	}
	// Clear default.
	if _, err := buildProfileDefault(ctx, app, commonOpts{Format: formatText}, "", true); err != nil {
		t.Fatalf("default clear: %v", err)
	}
	if reloadConfig(t, app).DefaultProfile != "" {
		t.Fatal("default not cleared")
	}
	// Bare read returns the current value without error.
	report, err := buildProfileDefault(ctx, app, commonOpts{Format: formatText}, "", false)
	if err != nil || report.Action != "default" {
		t.Fatalf("bare default read failed: %v %+v", err, report)
	}
}
