// Package codex implements the Codex CLI adapter. Auth mode swaps only
// ~/.codex/auth.json; the OS-keyring credential store is detect-only in
// v0.1.0 (see docs/ADAPTERS.md and docs/ROADMAP.md).
package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/freshness"
	"github.com/webkaz-labs/kagikae/internal/jwt"
	"github.com/webkaz-labs/kagikae/internal/keychain"
)

// KeychainService is the macOS Keychain item service Codex uses when
// cli_auth_credentials_store = "keyring" (discovery 2026-06-16, docs/ADAPTERS.md).
// The item's account is a per-login opaque id (`cli|<opaque>`), captured
// verbatim; the payload is the whole auth.json JSON.
const KeychainService = "Codex Auth"

type Codex struct{}

func init() { adapter.Register(Codex{}) }

func (Codex) ID() string { return constants.ToolCodex }

func (Codex) Binary() string { return "codex" }

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

func (c Codex) Artifacts(_ context.Context, env adapter.Env) ([]artifact.Spec, error) {
	if configuredStore(env) == "keyring" {
		// The keyring item's account is a per-login opaque id, so it is captured
		// verbatim (KeychainReplace) and apply deletes the prior item to keep a
		// single live item. The structure guard requires a JSON object holding
		// /tokens (the OAuth login shape; docs/ADAPTERS.md).
		return []artifact.Spec{{
			Name:            "auth",
			Kind:            constants.KindKeychain,
			Target:          KeychainService,
			Pointer:         "/tokens",
			KeychainReplace: true,
		}}, nil
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
		specs, err := c.Artifacts(ctx, env)
		if err != nil {
			return info, err
		}
		v, err := artifact.ReadLive(ctx, specs[0])
		if err != nil {
			return info, err
		}
		info.AuthPresent = v.Present
		if !v.Present {
			info.Warnings = append(info.Warnings,
				"no Codex Auth keychain item; log in with codex first")
		}
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

// authBytes returns the codex auth JSON from the active credential store —
// ~/.codex/auth.json for the file/auto store, the Codex Auth keychain payload
// for the keyring store — and a label used in error messages.
func authBytes(ctx context.Context, env adapter.Env) ([]byte, string, error) {
	if configuredStore(env) == "keyring" {
		data, found, err := keychain.ReadItem(ctx, KeychainService)
		if err != nil {
			return nil, KeychainService, err
		}
		if !found {
			return nil, KeychainService, fmt.Errorf("no %s keychain item", KeychainService)
		}
		return data, KeychainService, nil
	}
	path := authJSONPath(env)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, path, fmt.Errorf("read %s: %w", path, err)
	}
	return data, path, nil
}

// Identity reads the logged-in account from the active credential store (auth.json
// file or the Codex Auth keychain payload — both the same JSON) so
// `kae add codex` (no name) can default the account name: the id_token's email
// claim when present, else the account_id.
func (Codex) Identity(ctx context.Context, env adapter.Env) (string, error) {
	data, source, err := authBytes(ctx, env)
	if err != nil {
		return "", err
	}
	var doc struct {
		Tokens struct {
			IDToken   string `json:"id_token"`
			AccountID string `json:"account_id"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("parse %s: %w", source, err)
	}
	if email := jwtEmailClaim(doc.Tokens.IDToken); email != "" {
		return email, nil
	}
	if doc.Tokens.AccountID != "" {
		return doc.Tokens.AccountID, nil
	}
	return "", fmt.Errorf("no id_token email claim or account_id in %s", source)
}

// jwtEmailClaim decodes a JWT's payload and returns its "email" claim, or "".
// It is a best-effort read for account-name defaulting; an unparseable token
// yields "" so the caller falls back to account_id (then the explicit form).
func jwtEmailClaim(token string) string {
	payload, ok := jwt.Payload(token)
	if !ok {
		return ""
	}
	var claims struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return claims.Email
}

// Freshness reads tokens.refresh_token presence and the access (or id) token
// JWT expiry from a whole auth.json (the file-driver snapshot and the keyring
// payload are the same JSON). A file holding only OPENAI_API_KEY parses as
// Known with no expiry.
func (Codex) Freshness(payload []byte) freshness.Info {
	var doc struct {
		Tokens struct {
			AccessToken  string `json:"access_token"`
			IDToken      string `json:"id_token"`
			RefreshToken string `json:"refresh_token"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		return freshness.Info{}
	}
	info := freshness.Info{Known: true, HasRefresh: doc.Tokens.RefreshToken != ""}
	if exp, ok := freshness.JWTExpiry(doc.Tokens.AccessToken); ok {
		info.ExpiresAt = exp
	} else if exp, ok := freshness.JWTExpiry(doc.Tokens.IDToken); ok {
		info.ExpiresAt = exp
	}
	return info
}

func (c Codex) Doctor(ctx context.Context, env adapter.Env) []adapter.Check {
	tool := constants.ToolCodex
	checks := []adapter.Check{adapter.BinaryCheck(env, tool, "codex")}
	store := configuredStore(env)
	info, _ := c.Detect(ctx, env)
	if store == "keyring" {
		checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckCredentialStore,
			Status: constants.StatusOK, Message: "credential store: keyring (Codex Auth keychain item)"})
		if info.AuthPresent {
			checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckAuthPresent,
				Status: constants.StatusOK, Message: "Codex Auth keychain item found"})
		} else {
			checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckAuthPresent,
				Status: constants.StatusWarn, Message: "no Codex Auth keychain item; log in with codex first"})
		}
		return checks
	}
	checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckCredentialStore,
		Status: constants.StatusOK, Message: "credential store: " + store + " (auth.json)"})
	if info.AuthPresent {
		checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckAuthPresent,
			Status: constants.StatusOK, Message: "auth.json found"})
	} else {
		checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckAuthPresent,
			Status:  constants.StatusWarn,
			Message: "no auth.json; log in with codex first (or the keyring store is silently in use)"})
	}
	return checks
}
