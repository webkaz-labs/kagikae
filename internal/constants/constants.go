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

// CompanionKnobExpectedLogin is the reserved, non-secret companion knob holding
// the login a token companion's stored token must resolve to. kae fills it in at
// `kae companion add` time from the token's live identity (via the spec's
// LoginProbe), and doctor's companion_token_drift check compares the live login
// against it. It is deliberately not a Spec.Knob, so users cannot set it
// directly and it is never delivered as an environment variable.
const CompanionKnobExpectedLogin = "expected_login"

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
	CheckBinaryPresent   = "binary_present"
	CheckAuthPresent     = "auth_present"
	CheckDriver          = "driver"
	CheckEnvConflict     = "env_conflict"
	CheckCredentialStore = "credential_store"
	CheckSecretBackend   = "secret_backend"
	CheckConfigValid     = "config_valid"
	CheckUnsupported     = "unsupported"
	CheckFileMode        = "file_mode"
	CheckCredentialStale = "credential_stale"
	// CheckCredentialExpiring: a captured snapshot still works but will need an
	// interactive re-login within the lead time. Deliberately its own code rather
	// than a second band of credential_stale: that code means "cannot open a
	// session", and a consumer filtering on it to find broken accounts must not
	// start matching accounts that are fine for another five days.
	CheckCredentialExpiring = "credential_expiring"
	CheckSecretOrphan       = "secret_orphan"
	CheckCompanionMissing   = "companion_missing" // a bound token knob has no stored secret
	CheckCompanionBinary    = "companion_binary"  // a bound companion's CLI is not in PATH
	CheckCompanionDrift     = "companion_drift"   // live git identity differs from the bound one
	// CheckCompanionTokenDrift: the live login a bound token resolves to differs
	// from the companion's expected_login. opt-in (it needs a network call).
	CheckCompanionTokenDrift = "companion_token_drift"
	// CheckIdentityDrift: a tool's live identity-only artifact no longer names the
	// account kae applied — someone or something outside kae rewrote it. Offline:
	// stored against live, compared on the spec's IdentityKeys (the keys that name
	// the account) and not byte-wise, since the tool renews the rest on its own.
	CheckIdentityDrift = "identity_drift"
	// CheckUpstreamVersion: the installed tool is a newer major/minor than the
	// version its adapter's behaviour assumptions were verified against.
	CheckUpstreamVersion = "upstream_version"
	// CheckPinStale: a directory bound with `kae pin` no longer exists, or binds
	// an account that no longer does.
	CheckPinStale = "pin_stale"
	// CheckActiveOrphan: kae cannot confirm the account state.json records as
	// active — no snapshot by that name, one whose metadata will not parse, or a
	// state file it cannot read at all.
	CheckActiveOrphan = "active_orphan"
)

// Credential freshness states, the `credential` field of a `kae ls` /
// `kae accounts` account row and a `kae status` tool row. Absent (omitempty)
// means kae could not judge the snapshot: an opaque payload, one that records no
// usable deadline, or a secret store it could not read. The two non-ok states
// mirror the credential_stale / credential_expiring doctor checks exactly — they
// read the same predicates, so a row and a check can never disagree.
const (
	CredentialOK       = "ok"
	CredentialExpiring = "expiring"
	CredentialStale    = "stale"
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
