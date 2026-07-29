// Package claude implements the Claude Code adapter. Auth mode switches the
// /claudeAiOauth credential (credentials file or macOS Keychain payload) plus
// /oauthAccount, the identity cache inside the mixed-state ~/.claude.json.
//
// The cache is switched because Claude Code's self-heal of it is TTL-gated, not
// unconditional: on startup it refetches the profile and rewrites emailAddress
// only when the cached object is incomplete or its profileFetchedAt is more than
// 24h old, and a token refresh renews profileFetchedAt without rewriting
// emailAddress or accountUuid. A credential in daily use therefore refreshes
// well inside the TTL, and a switched token would leave the previous account's
// identity displayed indefinitely.
//
// Two consequences kae depends on. The switched identity's lifetime is the
// snapshot's: kae applies the profileFetchedAt that was live at capture time, so
// a snapshot older than 24h makes claude refetch on its next start (it then
// writes the right email for the applied token by itself). And because that
// refetch rewrites profileFetchedAt and the plan fields, a live-vs-applied
// identity comparison must be keyed to IdentityKeys instead of comparing bytes.
// See docs/ADAPTERS.md.
package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/freshness"
)

// KeychainService is Claude Code's macOS Keychain item service name.
const KeychainService = "Claude Code-credentials"

// envConflicts override subscription login inside Claude Code.
var envConflicts = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN"}

type Claude struct{}

func init() { adapter.Register(Claude{}) }

func (Claude) ID() string { return constants.ToolClaude }

func (Claude) Binary() string { return "claude" }

// VerifiedVersion is the Claude Code release kae's behaviour assumptions were
// last checked on (docs/VALIDATION.md "Upstream Behaviour Assumptions"). 2.1.220
// is where /oauthAccount's self-heal was measured as gated behind a 24h
// profileFetchedAt TTL that a token refresh keeps renewing — the finding kae's
// identity switch depends on — so a newer minor is worth re-measuring.
func (Claude) VerifiedVersion() string { return "2.1.220" }

// configDir honors CLAUDE_CONFIG_DIR as the live base path when already set.
// Auth mode never sets it.
func configDir(env adapter.Env) string {
	if dir := env.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(env.Home, ".claude")
}

// claudeJSONPath is the mixed-state identity file. With CLAUDE_CONFIG_DIR
// set, Claude Code keeps it inside that directory.
func claudeJSONPath(env adapter.Env) string {
	if dir := env.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, ".claude.json")
	}
	return filepath.Join(env.Home, ".claude.json")
}

func credentialsPath(env adapter.Env) string {
	return filepath.Join(configDir(env), ".credentials.json")
}

func driver(env adapter.Env) (string, error) {
	// KAE_CLAUDE_DRIVER=file forces the file-patch driver even on darwin so
	// smoke/container checks close the round-trip on .credentials.json and
	// never touch the real login keychain. Read here, the override applies to
	// both the capture (kae add) and apply (kae use) paths. The value may come
	// straight from the env or, when unset, from [tools.claude] driver via the
	// app-layer Getenv shim (see internal/cmd/app.go claudeDriverGetenv). Reject
	// any other value rather than silently ignoring a typo. See docs/ADAPTERS.md.
	if v := env.Getenv(constants.EnvKaeClaudeDriver); v != "" {
		if v == constants.DriverValueFile {
			return constants.DriverClaudeFilePatch, nil
		}
		return "", fmt.Errorf("%w: %s=%q is invalid (only %q is supported)",
			adapter.ErrUnsupported, constants.EnvKaeClaudeDriver, v, constants.DriverValueFile)
	}
	switch env.GOOS {
	case "darwin":
		return constants.DriverClaudeKeychainPatch, nil
	case "linux":
		return constants.DriverClaudeFilePatch, nil
	default:
		return "", fmt.Errorf("%w: claude auth switching is not supported on %s", adapter.ErrUnsupported, env.GOOS)
	}
}

// oauthAccountSpec switches /oauthAccount, claude's identity cache, inside the
// mixed-state ~/.claude.json — by JSON pointer only, so projects, mcpServers,
// onboarding and every other key in that file are untouched. IdentityOnly: it
// records who is logged in without being part of what authenticates, so losing
// it is safe (claude refetches the profile from the live token) and its presence
// alone is not a login.
func oauthAccountSpec(env adapter.Env) artifact.Spec {
	return artifact.Spec{
		Name:         "oauth_account",
		Kind:         constants.KindJSONPointer,
		Target:       claudeJSONPath(env),
		Pointer:      "/oauthAccount",
		IdentityOnly: true,
		// The keys /login rewrites unconditionally, and exactly the ones a token
		// refresh never touches — so they are what "is this still the account kae
		// applied?" may look at. The rest of the object (profileFetchedAt, plan and
		// billing fields) is refreshed by claude on its own schedule and comparing
		// it would flag a correct switch as drift.
		IdentityKeys: []string{"accountUuid", "emailAddress", "organizationUuid"},
	}
}

// Artifacts returns the credential first, then the identity cache. Detect reads
// specs[0] as the credential (keychainCredForBond selects by kind instead, and
// credentialArtifactName keeps its own name map), so the order is a contract of
// this adapter, pinned by TestClaudeArtifactsLinux/Darwin.
func (c Claude) Artifacts(_ context.Context, env adapter.Env) ([]artifact.Spec, error) {
	drv, err := driver(env)
	if err != nil {
		return nil, err
	}
	credential := artifact.Spec{
		Name:    "claude_ai_oauth",
		Kind:    constants.KindJSONPointer,
		Target:  credentialsPath(env),
		Pointer: "/claudeAiOauth",
	}
	if drv == constants.DriverClaudeKeychainPatch {
		credential = artifact.Spec{
			Name:            "claude_ai_oauth",
			Kind:            constants.KindKeychain,
			Target:          KeychainService,
			Pointer:         "/claudeAiOauth",
			KeychainAccount: env.Getenv("USER"),
		}
	}
	return []artifact.Spec{credential, oauthAccountSpec(env)}, nil
}

func (c Claude) Detect(ctx context.Context, env adapter.Env) (adapter.Info, error) {
	info := adapter.Info{Tool: constants.ToolClaude, Warnings: []string{}}
	drv, err := driver(env)
	if err != nil {
		return info, err
	}
	info.Driver = drv
	if _, err := env.LookPath("claude"); err == nil {
		info.BinaryPresent = true
	}
	specs, err := c.Artifacts(ctx, env)
	if err != nil {
		return info, err
	}
	v, err := artifact.ReadLive(ctx, specs[0])
	if err != nil {
		return info, err
	}
	info.AuthPresent = v.Present
	for _, name := range envConflicts {
		if env.Getenv(name) != "" {
			info.Warnings = append(info.Warnings, name+" is set and overrides the switched login")
		}
	}
	return info, nil
}

// Identity reads oauthAccount.emailAddress from ~/.claude.json — claude's
// identity cache — so `kae add claude` (no name) can default the account name
// to the logged-in email. It is the one place claude records who is logged in;
// auth mode switches it as the oauth_account artifact, so it tracks the applied
// account instead of the one that happened to log in last.
func (Claude) Identity(_ context.Context, env adapter.Env) (string, error) {
	path := claudeJSONPath(env)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	var doc struct {
		OAuthAccount struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"oauthAccount"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	if doc.OAuthAccount.EmailAddress == "" {
		return "", fmt.Errorf("no oauthAccount.emailAddress in %s", path)
	}
	return doc.OAuthAccount.EmailAddress, nil
}

// Freshness reads claudeAiOauth's expiresAt (Unix ms), refreshToken and
// refreshTokenExpiresAt. The keychain payload wraps the object under
// claudeAiOauth; the file-driver snapshot stores the inner object directly, so
// both nestings are handled. Callers walk every stored artifact and take the
// first datable one, so a payload without expiresAt (the oauth_account identity
// cache) must report Known=false rather than a zero expiry, which would read as
// long expired.
//
// refreshTokenExpiresAt is persisted next to expiresAt and matters because a
// Claude Code refresh token now lives days, not a month: without it, an expired
// access token plus any refresh string reads as "recoverable" long after it is
// not. Claude Code itself warns on it (it surfaces the remaining days inside 3).
//
// The tombstone is the other half. When a refresh fails with invalid_grant,
// Claude Code overwrites the credential in place with blank tokens and
// expiresAt 0 — an explicit death certificate. Read literally that is "no expiry
// recorded", the most harmless state kae has, so it is translated here (the one
// place that knows this payload) into Invalid.
func (Claude) Freshness(payload []byte) freshness.Info {
	root, ok := freshness.DecodeObject(payload)
	if !ok {
		return freshness.Info{}
	}
	obj := root
	if inner, ok := root["claudeAiOauth"]; ok {
		if nested, ok := freshness.DecodeObject(inner); ok {
			obj = nested
		}
	}
	if _, ok := obj["expiresAt"]; !ok {
		return freshness.Info{}
	}
	hasAccess := freshness.NonEmptyString(obj["accessToken"])
	hasRefresh := freshness.NonEmptyString(obj["refreshToken"])
	return freshness.Info{
		Known:            true,
		ExpiresAt:        freshness.EpochToTime(freshness.NumberFrom(obj["expiresAt"])),
		HasRefresh:       hasRefresh,
		RefreshExpiresAt: freshness.EpochToTime(freshness.NumberFrom(obj["refreshTokenExpiresAt"])),
		// Both tokens blank in a payload that still carries expiresAt: nothing is
		// left to authenticate or refresh with, which is what the tombstone is.
		Invalid: !hasAccess && !hasRefresh,
	}
}

func (c Claude) Doctor(ctx context.Context, env adapter.Env) []adapter.Check {
	tool := constants.ToolClaude
	if _, err := driver(env); err != nil {
		// driver() fails either for an unsupported platform or for an invalid
		// KAE_CLAUDE_DRIVER value; its own message names the real cause, so
		// surface it verbatim rather than assuming a platform problem.
		return []adapter.Check{{
			Tool: tool, Code: constants.CheckUnsupported,
			Status: constants.StatusError, Message: err.Error(),
		}}
	}
	checks := []adapter.Check{adapter.BinaryCheck(env, tool, "claude")}
	info, err := c.Detect(ctx, env)
	switch {
	case err != nil:
		checks = append(checks, adapter.Check{
			Tool: tool, Code: constants.CheckAuthPresent,
			Status: constants.StatusError, Message: err.Error(),
		})
	case info.AuthPresent:
		checks = append(checks, adapter.Check{
			Tool: tool, Code: constants.CheckAuthPresent,
			Status: constants.StatusOK, Message: "live subscription credential found",
		})
	default:
		checks = append(checks, adapter.Check{
			Tool: tool, Code: constants.CheckAuthPresent,
			Status: constants.StatusWarn, Message: "no live subscription credential (log in with claude first)",
		})
	}
	checks = append(checks, adapter.Check{
		Tool: tool, Code: constants.CheckDriver,
		Status: constants.StatusOK, Message: "driver: " + info.Driver,
	})
	checks = append(checks, adapter.EnvConflictChecks(env, tool, envConflicts)...)
	// The macOS driver is keychain-based, but a stray plaintext credential
	// file with loose permissions deserves the warning there too.
	if check, ok := adapter.FileModeCheck(env, tool, credentialsPath(env)); ok {
		checks = append(checks, check)
	}
	return checks
}
