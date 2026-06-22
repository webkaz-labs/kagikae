// Package constants holds the JSON contract vocabulary: tool ids, drivers,
// artifact kinds, status tokens, error codes, and exit codes. Commands and
// adapters must use these constants instead of inline literals.
package constants

// SchemaVersion is the integer schema version of all stable JSON reports.
const SchemaVersion = 1

// Tool identifiers.
const (
	ToolClaude   = "claude"
	ToolCodex    = "codex"
	ToolAgy      = "agy"
	ToolOpencode = "opencode"
	ToolCursor   = "cursor"
	ToolCopilot  = "copilot"
)

// Tools is the canonical tool ordering for reports and iteration.
var Tools = []string{ToolClaude, ToolCodex, ToolAgy, ToolOpencode, ToolCursor, ToolCopilot}

// RemovedTools maps tools kae no longer supports to their successor, for
// error messages and config tolerance (docs/RELEASE.md Breaking Changes).
var RemovedTools = map[string]string{
	"gemini": ToolAgy, // upstream retired Gemini CLI for Antigravity (2026-05)
}

// Companion identifiers. Companions are auth-lockstep targets (git, gh, cloud
// CLIs) whose identity kae binds per profile by driving env/config — it does
// not capture their credentials the way it does Tools. The normative
// switched/preserved contract is docs/ADAPTERS-COMPANION.md.
const (
	CompanionGit        = "git"
	CompanionGH         = "gh"
	CompanionCloudflare = "cloudflare"
	CompanionKubectl    = "kubectl"
)

// Companions is the canonical companion ordering for reports and iteration.
var Companions = []string{CompanionGit, CompanionGH, CompanionCloudflare, CompanionKubectl}

// Companion override kinds: how a companion's identity is delivered.
//   - OverrideGitConfig: render a kae-owned git config file, point an env var
//     (GIT_CONFIG_GLOBAL) at it; the file [include]s the user's own config.
//   - OverrideToken: secret env var(s) resolved at mise eval time via an
//     exec() lookup against the secret backend (never written to disk).
//   - OverrideConfigDir: env var(s) point at a user-provided config path.
const (
	OverrideGitConfig = "git-config"
	OverrideToken     = "token"
	OverrideConfigDir = "config-dir"
)

// Switch modes / isolation kinds. The mechanism vocabulary is unified on
// shared/isolated (docs/RELEASE.md v0.8.0): the per-directory bind kinds match
// the user-facing -s/-i flags and the on-disk path segments.
const (
	ModeAuth     = "auth"     // global shared (real home; bare use, run -s)
	ModeEnv      = "env"      // env-profile injection (run --env)
	ModeShared   = "shared"   // per-directory shared (kae pin --shared)
	ModeIsolated = "isolated" // per-directory isolated (kae pin --isolated)
	ModeSync     = "sync"     // global isolated (kae use --isolated)
)

// EnvKaeProfile is the environment variable that pins a kae profile to a
// directory (rendered by kae mise init / kae pin, read by bare kae use).
const EnvKaeProfile = "KAE_PROFILE"

// EnvKaeClaudeDriver overrides the claude credential driver. Set to
// DriverValueFile to force the file-patch driver (.credentials.json under
// CLAUDE_CONFIG_DIR) even on darwin, so smoke/container checks never touch
// the real login keychain. It is an ephemeral escape hatch: a live macOS
// claude reads the keychain, not the file, so persisting it would break a
// real login. See docs/ADAPTERS.md and docs/VALIDATION.md.
const EnvKaeClaudeDriver = "KAE_CLAUDE_DRIVER"

// DriverValueFile is the only accepted value for EnvKaeClaudeDriver (and the
// [tools.claude] driver config option): force the file-patch driver.
const DriverValueFile = "file"

// Driver identifiers.
const (
	DriverClaudeFilePatch      = "claude-file-patch"
	DriverClaudeKeychainPatch  = "claude-keychain-patch"
	DriverCodexAuthJSON        = "codex-auth-json"
	DriverCodexKeyring         = "codex-keyring"
	DriverAgyFileSnapshot      = "agy-file-snapshot"
	DriverAgyKeychain          = "agy-keychain"
	DriverOpencodeFilePatch    = "opencode-file-patch"
	DriverCursorKeychain       = "cursor-keychain"
	DriverCopilotConfigPointer = "copilot-config-pointer"
)

// Artifact kinds.
const (
	KindJSONPointer = "json-pointer"
	KindFile        = "file"
	KindKeychain    = "keychain"
)

// Check status tokens for doctor and warnings.
const (
	StatusOK      = "ok"
	StatusWarn    = "warn"
	StatusError   = "error"
	StatusSkipped = "skipped"
)

// Doctor check codes.
const (
	CheckBinaryPresent    = "binary_present"
	CheckAuthPresent      = "auth_present"
	CheckDriver           = "driver"
	CheckEnvConflict      = "env_conflict"
	CheckCredentialStore  = "credential_store"
	CheckSecretBackend    = "secret_backend"
	CheckConfigValid      = "config_valid"
	CheckUnsupported      = "unsupported"
	CheckFileMode         = "file_mode"
	CheckCredentialStale  = "credential_stale"
	CheckSecretOrphan     = "secret_orphan"
	CheckCompanionMissing = "companion_missing" // a bound token knob has no stored secret
	CheckCompanionBinary  = "companion_binary"  // a bound companion's CLI is not in PATH
	CheckCompanionDrift   = "companion_drift"   // live git identity differs from the bound one
)

// Exit codes and their stable error-code tokens.
const (
	ExitOK            = 0
	ExitError         = 1
	ExitInvalidConfig = 2
	ExitAuthMissing   = 3
	ExitLockBusy      = 4
	ExitUnsupported   = 5
	ExitCLIMissing    = 6
	ExitNotFound      = 7
	ExitPermission    = 8
	ExitSecretStore   = 9
	ExitUnsafeRefused = 10
	ExitAuthUnchanged = 11
	ExitUsage         = 64
)

// Error-code tokens used in JSON error reports.
const (
	CodeOK            = "ok"
	CodeError         = "error"
	CodeInvalidConfig = "invalid_config"
	CodeAuthMissing   = "auth_missing"
	CodeLockBusy      = "lock_busy"
	CodeUnsupported   = "unsupported"
	CodeCLIMissing    = "cli_missing"
	CodeNotFound      = "not_found"
	CodePermission    = "permission"
	CodeSecretStore   = "secret_store"
	CodeUnsafeRefused = "unsafe_refused"
	CodeAuthUnchanged = "auth_unchanged"
	CodeUsage         = "usage"
)

// ErrorCode returns the stable token for an exit code.
func ErrorCode(exit int) string {
	switch exit {
	case ExitOK:
		return CodeOK
	case ExitInvalidConfig:
		return CodeInvalidConfig
	case ExitAuthMissing:
		return CodeAuthMissing
	case ExitLockBusy:
		return CodeLockBusy
	case ExitUnsupported:
		return CodeUnsupported
	case ExitCLIMissing:
		return CodeCLIMissing
	case ExitNotFound:
		return CodeNotFound
	case ExitPermission:
		return CodePermission
	case ExitSecretStore:
		return CodeSecretStore
	case ExitUnsafeRefused:
		return CodeUnsafeRefused
	case ExitAuthUnchanged:
		return CodeAuthUnchanged
	case ExitUsage:
		return CodeUsage
	default:
		return CodeError
	}
}

// IsTool reports whether name is a known tool id.
func IsTool(name string) bool {
	for _, t := range Tools {
		if t == name {
			return true
		}
	}
	return false
}

// IsCompanion reports whether name is a known companion id.
func IsCompanion(name string) bool {
	for _, c := range Companions {
		if c == name {
			return true
		}
	}
	return false
}
