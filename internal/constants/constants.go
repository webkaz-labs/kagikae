// Package constants holds the JSON contract vocabulary: tool ids, drivers,
// artifact kinds, status tokens, error codes, and exit codes. Commands and
// adapters must use these constants instead of inline literals.
package constants

// SchemaVersion is the integer schema version of all stable JSON reports.
const SchemaVersion = 1

// Tool identifiers.
const (
	ToolClaude = "claude"
	ToolCodex  = "codex"
	ToolGemini = "gemini"
	ToolAgy    = "agy"
)

// Tools is the canonical tool ordering for reports and iteration.
var Tools = []string{ToolClaude, ToolCodex, ToolGemini, ToolAgy}

// Switch modes. Only ModeAuth is implemented in v0.1.0.
const (
	ModeAuth = "auth"
)

// Driver identifiers.
const (
	DriverClaudeFilePatch     = "claude-file-patch"
	DriverClaudeKeychainPatch = "claude-keychain-patch"
	DriverCodexAuthJSON       = "codex-auth-json"
	DriverCodexKeyring        = "codex-keyring"
	DriverGeminiOAuthCache    = "gemini-oauth-cache"
	DriverAgyFileSnapshot     = "agy-file-snapshot"
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
	CheckTransitionNotice = "transition_notice"
	CheckUnsupported      = "unsupported"
	CheckFileMode         = "file_mode"
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
