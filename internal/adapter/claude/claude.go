// Package claude implements the Claude Code adapter. Auth mode switches only
// the /claudeAiOauth credential (credentials file or macOS Keychain payload).
// /oauthAccount in ~/.claude.json is a token-derived identity cache that
// claude self-heals on startup; it is not an auth artifact and is not switched.
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

func (c Claude) Artifacts(_ context.Context, env adapter.Env) ([]artifact.Spec, error) {
	drv, err := driver(env)
	if err != nil {
		return nil, err
	}
	if drv == constants.DriverClaudeKeychainPatch {
		return []artifact.Spec{
			{
				Name:            "claude_ai_oauth",
				Kind:            constants.KindKeychain,
				Target:          KeychainService,
				Pointer:         "/claudeAiOauth",
				KeychainAccount: env.Getenv("USER"),
			},
		}, nil
	}
	return []artifact.Spec{
		{
			Name:    "claude_ai_oauth",
			Kind:    constants.KindJSONPointer,
			Target:  credentialsPath(env),
			Pointer: "/claudeAiOauth",
		},
	}, nil
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
// token-derived identity cache — so `kae add claude` (no name) can default the
// account name to the logged-in email. Auth mode never switches this field, but
// it is the one place claude records who is logged in.
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

// Freshness reads claudeAiOauth's expiresAt (Unix ms) and refreshToken. The
// keychain payload wraps the object under claudeAiOauth; the file-driver
// snapshot stores the inner object directly, so both nestings are handled.
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
	return freshness.Info{
		Known:      true,
		ExpiresAt:  freshness.EpochToTime(freshness.NumberFrom(obj["expiresAt"])),
		HasRefresh: freshness.NonEmptyString(obj["refreshToken"]),
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
