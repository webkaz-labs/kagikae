package cmd

import (
	"context"
	"fmt"
	"strings"
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

func TestProfileUnsetUsesCurrentMappings(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		t.Run(fmt.Sprint(dryRun), func(t *testing.T) {
			app := testApp(t, nil)
			writeConfigFile(t, app, "version = 1\n[profiles.main.accounts]\nclaude = \"main\"\n")
			current := "version = 1\n# preserve this comment\n[profiles.main.accounts]\nclaude = \"main\"\ncodex = \"side\"\n"
			writeFile(t, app.ConfigPath, current)
			if _, err := buildProfileUnset(context.Background(), app, commonOpts{DryRun: dryRun}, "main", "claude"); err != nil {
				t.Fatal(err)
			}
			cfg := reloadConfig(t, app)
			if cfg.Profiles["main"].Accounts["codex"] != "side" {
				t.Fatal("concurrent mapping lost")
			}
			if dryRun {
				if readFile(t, app.ConfigPath) != current {
					t.Fatal("dry-run wrote config")
				}
			} else if _, exists := cfg.Profiles["main"].Accounts["claude"]; exists {
				t.Fatal("requested mapping retained")
			}
			if !strings.Contains(readFile(t, app.ConfigPath), "# preserve this comment") {
				t.Fatal("comment lost")
			}
		})
	}
}

func TestProfileRemoveUsesCurrentDefault(t *testing.T) {
	for _, force := range []bool{false, true} {
		for _, dryRun := range []bool{false, true} {
			t.Run(fmt.Sprintf("force=%v/dry=%v", force, dryRun), func(t *testing.T) {
				app := testApp(t, nil)
				writeConfigFile(t, app, "version = 1\n[profiles.main.accounts]\nclaude = \"main\"\n")
				current := "version = 1\ndefault_profile = \"main\"\n[profiles.main.accounts]\nclaude = \"main\"\n"
				writeFile(t, app.ConfigPath, current)
				_, err := buildProfileRm(context.Background(), app, commonOpts{DryRun: dryRun}, "main", force)
				if !force {
					if exitOf(err) != constants.ExitUnsafeRefused {
						t.Fatalf("expected refusal: %v", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
				if !force || dryRun {
					if readFile(t, app.ConfigPath) != current {
						t.Fatal("refusal/preview wrote config")
					}
				} else {
					cfg := reloadConfig(t, app)
					if cfg.DefaultProfile != "" {
						t.Fatal("dangling default")
					}
					if _, exists := cfg.Profiles["main"]; exists {
						t.Fatal("profile retained")
					}
				}
			})
		}
	}
}

func TestProfileDecisionsRejectConcurrentlyRemovedProfile(t *testing.T) {
	for _, operation := range []string{"unset", "rm", "default"} {
		t.Run(operation, func(t *testing.T) {
			app := testApp(t, nil)
			writeConfigFile(t, app, "version = 1\n[profiles.main.accounts]\nclaude = \"main\"\n")
			current := "version = 1\n# profile removed\n"
			writeFile(t, app.ConfigPath, current)
			var err error
			switch operation {
			case "unset":
				_, err = buildProfileUnset(context.Background(), app, commonOpts{}, "main", "claude")
			case "rm":
				_, err = buildProfileRm(context.Background(), app, commonOpts{}, "main", false)
			case "default":
				_, err = buildProfileDefault(context.Background(), app, commonOpts{}, "main", false)
			}
			if exitOf(err) != constants.ExitNotFound {
				t.Fatalf("expected not found: %v", err)
			}
			if readFile(t, app.ConfigPath) != current {
				t.Fatal("refusal wrote config")
			}
		})
	}
}
