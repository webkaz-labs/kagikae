// Package codex implements the Codex CLI adapter. Auth mode swaps the credential
// store codex resolves for this codex home — CODEX_HOME/auth.json, or this home's
// `Codex Auth` keychain item — and nothing else under CODEX_HOME
// (see docs/ADAPTERS.md).
package codex

import (
	"context"
	"crypto/sha256"
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

// KeychainService is the macOS Keychain item service Codex uses when the
// resolved credential store is the keyring (docs/ADAPTERS.md). The payload is the
// whole auth.json JSON; the item's *account* attribute is derived from
// CODEX_HOME (storeKey), which is what scopes one item to one codex home.
const KeychainService = "Codex Auth"

// Credential store modes codex accepts for cli_auth_credentials_store. The
// spellings are upstream's serde names, and `file` is upstream's default for an
// absent key — not `auto`.
const (
	storeFile      = "file"
	storeKeyring   = "keyring"
	storeAuto      = "auto"
	storeEphemeral = "ephemeral"
)

// storeKey returns the keychain item's account attribute for this environment:
// `cli|` + the first 16 hex characters of sha256 over the **symlink-resolved,
// absolute** CODEX_HOME. It is a rule, not a constant, and it is the whole
// per-home scoping of codex's credential.
//
// Deliberately not claude's rule, and the difference is the point: claude hashes
// the raw env string NFC-normalized with no path resolution, codex hashes a
// canonicalized path and takes 16 characters rather than 8. Reusing either for
// the other writes an item the tool never reads.
//
// Modelling the account as an opaque per-login id kae had to capture verbatim is
// what made a switch destructive: kae read and deleted `Codex Auth` by service
// alone, while codex reads and deletes it by service **and** this account — so a
// second codex home holds a second legitimate item, and a kae switch removed it.
//
// The fallback for an unresolvable path mirrors upstream's own: codex hashes the
// unresolved path when canonicalize fails, which it reaches on a machine where
// CODEX_HOME is unset and `~/.codex` does not exist yet. It is unreachable for a
// spec kae actually uses, and deliberately so rather than by luck: the keyring and
// `auto` stores are only resolved by reading `config.toml` *inside* that directory,
// so the directory exists whenever this hash is asked for. Returning an error here
// instead would therefore trade upstream parity for a branch nothing takes.
// docs/VALIDATION.md § "Upstream Behaviour Assumptions" carries the rule and its
// login-free verification.
func storeKey(env adapter.Env) string {
	home := codexHome(env)
	if abs, err := filepath.Abs(home); err == nil {
		home = abs
	}
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	sum := sha256.Sum256([]byte(home))
	return fmt.Sprintf("cli|%x", sum[:8])
}

// A **relative** CODEX_HOME diverges harder than claude's variable, which is why
// the two get different wording. codex canonicalizes its home before using it —
// measured at 0.145.0: from one directory `CODEX_HOME=relcfg` reported
// `/private/var/.../relcfg/config.toml`, i.e. resolved against **codex's** working
// directory and symlink-resolved — and that canonical path is what its keyring
// account hashes. So a relative value moves the `auth.json` store *and* the
// `Codex Auth` item, where claude's moves only files.
//
// It is also the one case that can fail loudly: from a directory with no `relcfg`
// in it, codex refuses to start ("CODEX_HOME points to \"relcfg\", but that path
// does not exist"). The divergence is therefore silent only when a directory of
// that name exists under both working directories — which is exactly the case a
// warning is for.
const relativeHomeWarning = "CODEX_HOME is relative: codex canonicalizes it against its own working" +
	" directory, so both the store kae writes and the keyring account it derives from that path" +
	" can differ from codex's — set an absolute path. codex refuses to start outright when the" +
	" relative path does not exist in its working directory."

// relativeHomeWarnings is the Detect/Doctor payload for a relative CODEX_HOME:
// one warning, or none. Both surfaces read it so neither can drift.
func relativeHomeWarnings(env adapter.Env) []string {
	if adapter.IsRelativeEnv(env, "CODEX_HOME") {
		return []string{relativeHomeWarning}
	}
	return nil
}

type Codex struct{}

func init() { adapter.Register(Codex{}) }

func (Codex) ID() string { return constants.ToolCodex }

func (Codex) Binary() string { return "codex" }

// VerifiedVersion is the Codex CLI release kae's behaviour assumptions were last
// checked on (docs/VALIDATION.md "Upstream Behaviour Assumptions").
func (Codex) VerifiedVersion() string { return "0.145.0" }

// codexHome honors CODEX_HOME as the live base path when already set.
func codexHome(env adapter.Env) string {
	if dir := env.Getenv("CODEX_HOME"); dir != "" {
		return dir
	}
	return filepath.Join(env.Home, ".codex")
}

func authJSONPath(env adapter.Env) string { return filepath.Join(codexHome(env), "auth.json") }

// codexConfig is the part of config.toml that decides where the credential lives.
type codexConfig struct {
	Store    string `toml:"cli_auth_credentials_store"`
	Features struct {
		SecretAuthStorage bool `toml:"secret_auth_storage"`
	} `toml:"features"`
}

// configuredStore returns the credential store codex resolves for this
// environment, or an error when kae cannot switch that store.
//
// The mapping is upstream's, not kae's: an absent key is `file` (the enum's
// default), `keyring` is keyring-only, `auto` reads the keyring first and falls
// back to the file, and `ephemeral` keeps the credential in memory for one
// process. Folding everything that is not `keyring` into the file store made
// `auto` the codex-shaped version of the macOS pin defect — kae writes auth.json,
// codex reads the keyring item first, and every offline guard stays green — so an
// unrecognized or unswitchable value is refused here instead of guessed at.
func configuredStore(env adapter.Env) (string, error) {
	path := filepath.Join(codexHome(env), "config.toml")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return storeFile, nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	var cfg codexConfig
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	store := cfg.Store
	if store == "" {
		store = storeFile
	}
	switch store {
	case storeFile:
		return storeFile, nil
	case storeKeyring, storeAuto:
		// `[features] secret_auth_storage` swaps the keyring backend for an
		// encrypted secrets file whose key alone lives in the keyring, so the
		// credential is not in the `Codex Auth` item at all. Refuse rather than
		// switch an item nothing reads. (Upstream default: off except on Windows.)
		if cfg.Features.SecretAuthStorage {
			return "", fmt.Errorf(
				"%w: codex [features] secret_auth_storage keeps the credential in an encrypted secrets file, not the %q keychain item",
				adapter.ErrUnsupported, KeychainService,
			)
		}
		// kae reads a keyring only through the macOS `security` CLI, so the
		// keyring-*only* store is unswitchable elsewhere. Refuse it here, at the
		// declaration point, rather than hand back a keychain spec whose every
		// operation fails with a raw `security` error — the same shape claude's
		// driver() uses for an unsupported platform. `auto` stays supported: it
		// resolves to auth.json, which is where codex falls back with no keyring.
		if store == storeKeyring && env.GOOS != "darwin" {
			return "", fmt.Errorf(
				"%w: codex cli_auth_credentials_store = %q keeps the credential in the OS keyring, which kae can only read on macOS (this is %s)",
				adapter.ErrUnsupported, storeKeyring, env.GOOS,
			)
		}
		return store, nil
	case storeEphemeral:
		return "", fmt.Errorf(
			"%w: codex cli_auth_credentials_store = %q keeps the credential in memory for one process, so there is nothing to capture or switch",
			adapter.ErrUnsupported, storeEphemeral,
		)
	default:
		return "", fmt.Errorf(
			"%w: codex cli_auth_credentials_store = %q is not one of %q, %q, %q, %q",
			adapter.ErrUnsupported, store, storeFile, storeKeyring, storeAuto, storeEphemeral,
		)
	}
}

// keyringSpec declares the `Codex Auth` item as it applies to this codex home.
// The account is derived (storeKey) and the spec is account-scoped, so every
// read, write and delete touches this home's item and never another home's — the
// two coexist under one service name. The structure guard requires a JSON object
// holding /tokens (the OAuth login shape; docs/ADAPTERS.md).
func keyringSpec(account string) artifact.Spec {
	return artifact.Spec{
		Name:                 "auth",
		Kind:                 constants.KindKeychain,
		Target:               KeychainService,
		Pointer:              "/tokens",
		KeychainAccount:      account,
		KeychainMatchAccount: true,
	}
}

func fileSpec(env adapter.Env) artifact.Spec {
	return artifact.Spec{
		Name:   "auth",
		Kind:   constants.KindFile,
		Target: authJSONPath(env),
	}
}

// usesKeyring reports whether the resolved store puts the credential in the
// keychain item rather than auth.json.
//
// `keyring` always does. `auto` is decided the way codex decides it at read
// time — the item wins when it exists, the file only when it does not — so kae
// follows the live state instead of picking a side: with no item, codex reads
// (and kae writes) auth.json; once codex's first save creates the item and
// deletes the file, both move to the item. The probe reads attributes only, so it
// never touches a payload.
//
// account is this codex home's derived item account, passed in rather than
// recomputed so one resolution resolves the path once (storeKey is two syscalls).
func usesKeyring(ctx context.Context, env adapter.Env, store, account string) (bool, error) {
	switch store {
	case storeKeyring:
		return true, nil
	case storeAuto:
		// kae's keychain access is the macOS `security` CLI; elsewhere `auto`
		// keeps resolving to the file, which is where codex falls back when no
		// keyring is reachable.
		if env.GOOS != "darwin" {
			return false, nil
		}
		return keychain.ItemExistsForAccount(ctx, KeychainService, account)
	}
	return false, nil
}

func (c Codex) Artifacts(ctx context.Context, env adapter.Env) ([]artifact.Spec, error) {
	store, err := configuredStore(env)
	if err != nil {
		return nil, err
	}
	account := storeKey(env)
	keyring, err := usesKeyring(ctx, env, store, account)
	if err != nil {
		return nil, err
	}
	if keyring {
		return []artifact.Spec{keyringSpec(account)}, nil
	}
	return []artifact.Spec{fileSpec(env)}, nil
}

func (c Codex) Detect(ctx context.Context, env adapter.Env) (adapter.Info, error) {
	info := adapter.Info{Tool: constants.ToolCodex, Driver: constants.DriverCodexAuthJSON, Warnings: []string{}}
	info.Warnings = append(info.Warnings, relativeHomeWarnings(env)...)
	if _, err := env.LookPath("codex"); err == nil {
		info.BinaryPresent = true
	}
	store, err := configuredStore(env)
	if err != nil {
		return info, err
	}
	specs, err := c.Artifacts(ctx, env)
	if err != nil {
		return info, err
	}
	if specs[0].Kind == constants.KindKeychain {
		info.Driver = constants.DriverCodexKeyring
		v, err := artifact.ReadLive(ctx, specs[0])
		if err != nil {
			return info, err
		}
		info.AuthPresent = v.Present
		if !v.Present {
			info.Warnings = append(info.Warnings,
				"no Codex Auth keychain item for this codex home; log in with codex first")
		}
		return info, nil
	}
	if _, err := os.Stat(specs[0].Target); err == nil {
		info.AuthPresent = true
	} else if store == storeAuto {
		// Under `auto` on macOS the keychain item was already probed while resolving
		// the spec, so its absence is established. Elsewhere kae cannot read the
		// keyring at all, so it must not claim the item is missing.
		if env.GOOS == "darwin" {
			info.Warnings = append(info.Warnings,
				"no auth.json and no "+KeychainService+" keychain item for this codex home; log in with codex first")
		} else {
			info.Warnings = append(info.Warnings,
				"no auth.json found; either codex is not logged in or a keyring kae cannot read on this platform holds the credential")
		}
	}
	return info, nil
}

// authBytes returns the codex auth JSON from the store the adapter resolved —
// auth.json, or this codex home's Codex Auth keychain payload — and a label used
// in error messages.
//
// It reads the resolved spec rather than re-deciding the store, so the account
// scoping (and every other part of the rule) lives in one place. The read is
// deliberately raw instead of artifact.ReadLive: identity detection tolerates a
// payload the switch guard would refuse, such as an API-key-only login with no
// /tokens object.
func authBytes(ctx context.Context, env adapter.Env) ([]byte, string, error) {
	specs, err := Codex{}.Artifacts(ctx, env)
	if err != nil {
		return nil, "", err
	}
	sp := specs[0]
	if sp.Kind == constants.KindKeychain {
		data, found, err := keychain.ReadItemForAccount(ctx, sp.Target, sp.KeychainAccount)
		if err != nil {
			return nil, sp.Target, err
		}
		if !found {
			return nil, sp.Target, fmt.Errorf("no %s keychain item for this codex home", sp.Target)
		}
		return data, sp.Target, nil
	}
	data, err := os.ReadFile(sp.Target)
	if err != nil {
		return nil, sp.Target, fmt.Errorf("read %s: %w", sp.Target, err)
	}
	return data, sp.Target, nil
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
	store, err := configuredStore(env)
	if err == nil {
		var specs []artifact.Spec
		if specs, err = c.Artifacts(ctx, env); err == nil {
			checks := append([]adapter.Check{adapter.BinaryCheck(env, tool, "codex")},
				storeChecks(ctx, tool, store, specs[0])...)
			return append(checks, adapter.EnvConflictChecksFrom(tool, relativeHomeWarnings(env))...)
		}
	}
	// configuredStore and Artifacts fail for an unswitchable store mode, an
	// unreadable config.toml, or a failed keychain probe; each message names its
	// own cause, so surface it verbatim rather than assuming one of them.
	return []adapter.Check{{
		Tool: tool, Code: constants.CheckUnsupported,
		Status: constants.StatusError, Message: err.Error(),
	}}
}

// storeChecks reports which store the credential resolved to and whether the
// credential is there. Under `auto` the resolved store is the informative half:
// the configured value alone does not say which of the two codex will read.
//
// Presence is read from the resolved spec rather than through Detect: Detect
// re-derives the store from scratch, which under `auto` on macOS means a second
// config.toml parse and a second `security` probe inside one `kae doctor`.
func storeChecks(ctx context.Context, tool, store string, sp artifact.Spec) []adapter.Check {
	where := "auth.json"
	if sp.Kind == constants.KindKeychain {
		where = KeychainService + " keychain item for this codex home"
	}
	checks := []adapter.Check{{
		Tool: tool, Code: constants.CheckCredentialStore,
		Status: constants.StatusOK, Message: "credential store: " + store + " (" + where + ")",
	}}
	// A read error is reported as absent, exactly as Detect's swallowed error was.
	present := false
	if sp.Kind == constants.KindKeychain {
		if v, err := artifact.ReadLive(ctx, sp); err == nil {
			present = v.Present
		}
	} else if _, err := os.Stat(sp.Target); err == nil {
		present = true
	}
	if present {
		return append(checks, adapter.Check{
			Tool: tool, Code: constants.CheckAuthPresent,
			Status: constants.StatusOK, Message: where + " found",
		})
	}
	return append(checks, adapter.Check{
		Tool: tool, Code: constants.CheckAuthPresent,
		Status: constants.StatusWarn, Message: "no " + where + "; log in with codex first",
	})
}
