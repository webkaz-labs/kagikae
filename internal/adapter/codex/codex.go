// Package codex implements the Codex CLI adapter. Auth mode swaps only
// ~/.codex/auth.json; the OS-keyring credential store is detect-only in
// v0.1.0 (see docs/ADAPTERS.md and docs/ROADMAP.md).
package codex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

type Codex struct{}

func init() { adapter.Register(Codex{}) }

func (Codex) ID() string { return constants.ToolCodex }

// codexHome honors CODEX_HOME as the live base path when already set.
func codexHome(env adapter.Env) string {
	if dir := env.Getenv("CODEX_HOME"); dir != "" {
		return dir
	}
	return filepath.Join(env.Home, ".codex")
}

func authJSONPath(env adapter.Env) string { return filepath.Join(codexHome(env), "auth.json") }

// configuredStore reads cli_auth_credentials_store from config.toml.
// Returns "auto" when unset or the config file is missing/unreadable.
func configuredStore(env adapter.Env) string {
	var cfg struct {
		Store string `toml:"cli_auth_credentials_store"`
	}
	data, err := os.ReadFile(filepath.Join(codexHome(env), "config.toml"))
	if err != nil {
		return "auto"
	}
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return "auto"
	}
	if cfg.Store == "" {
		return "auto"
	}
	return cfg.Store
}

// keyringRefusal is the typed error for the detect-only keyring store.
func keyringRefusal() error {
	return fmt.Errorf("%w: codex cli_auth_credentials_store is \"keyring\", which kae cannot switch yet; set cli_auth_credentials_store = \"file\" in ~/.codex/config.toml and log in again, or wait for the codex-keyring driver", artifact.ErrUnsafe)
}

func (c Codex) Artifacts(_ context.Context, env adapter.Env) ([]artifact.Spec, error) {
	if configuredStore(env) == "keyring" {
		return nil, keyringRefusal()
	}
	return []artifact.Spec{{
		Name:   "auth",
		Kind:   constants.KindFile,
		Target: authJSONPath(env),
	}}, nil
}

func (c Codex) Detect(ctx context.Context, env adapter.Env) (adapter.Info, error) {
	info := adapter.Info{Tool: constants.ToolCodex, Driver: constants.DriverCodexAuthJSON, Warnings: []string{}}
	if _, err := env.LookPath("codex"); err == nil {
		info.BinaryPresent = true
	}
	store := configuredStore(env)
	if store == "keyring" {
		info.Driver = constants.DriverCodexKeyring
		info.Warnings = append(info.Warnings, "codex keyring credential store detected; switching is not supported yet")
		return info, nil
	}
	if _, err := os.Stat(authJSONPath(env)); err == nil {
		info.AuthPresent = true
	} else if store == "auto" {
		info.Warnings = append(info.Warnings,
			"no auth.json found; either codex is not logged in or the keyring store is in use")
	}
	return info, nil
}

func (c Codex) Doctor(ctx context.Context, env adapter.Env) []adapter.Check {
	tool := constants.ToolCodex
	checks := []adapter.Check{adapter.BinaryCheck(env, tool, "codex")}
	store := configuredStore(env)
	if store == "keyring" {
		checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckCredentialStore,
			Status: constants.StatusError,
			Message: "cli_auth_credentials_store = \"keyring\" is detect-only; kae cannot switch it yet (set it to \"file\" to use kae)"})
		return checks
	}
	checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckCredentialStore,
		Status: constants.StatusOK, Message: "credential store: " + store + " (auth.json)"})
	info, _ := c.Detect(ctx, env)
	if info.AuthPresent {
		checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckAuthPresent,
			Status: constants.StatusOK, Message: "auth.json found"})
	} else {
		checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckAuthPresent,
			Status: constants.StatusWarn,
			Message: "no auth.json; log in with codex first (or the keyring store is silently in use)"})
	}
	return checks
}
