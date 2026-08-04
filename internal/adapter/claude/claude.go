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
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"golang.org/x/text/unicode/norm"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/freshness"
)

// KeychainService is the base of Claude Code's macOS Keychain item service
// name — and the whole name only while CLAUDE_CONFIG_DIR is unset. Anything
// that needs the name for a specific environment must go through
// keychainService, never this constant.
const KeychainService = "Claude Code-credentials"

// EnvSecureStorageDir replaces CLAUDE_CONFIG_DIR as the input to *both* the
// keychain service name and the plaintext credential's directory whenever it is
// present in the environment. kae cannot model a credential location it does not
// control, so driver() refuses instead of guessing — see its doc comment.
const EnvSecureStorageDir = "CLAUDE_SECURESTORAGE_CONFIG_DIR"

// EnvCustomOAuthURL points claude at a non-production OAuth endpoint, and a
// non-empty value renames *both* stores: the build's OAuth suffix becomes
// "-custom-oauth", and that suffix sits inside the keychain service name
// ("Claude Code" + suffix + "-credentials" + the per-config-dir suffix) *and*
// inside the identity file name (".claude-custom-oauth.json", in the same config
// dir claudeJSONPath resolves). Measured on 2.1.220 from the bundle: the suffix
// comes from one function whose only environment-visible input is this variable,
// and claude enumerates all four suffixes ("", "-staging-oauth", "-local-oauth",
// "-custom-oauth") when it looks for its own identity files. docs/VALIDATION.md
// § "Upstream Behaviour Assumptions" carries the source lines and the procedure
// that reads them.
//
// The empty string is *not* the dangerous case here, unlike EnvSecureStorageDir:
// claude tests this value for truthiness, so an empty value changes nothing and
// refusing on it would be a false refusal.
//
// kae refuses rather than models the suffix because the other three values come
// from the build channel, which a released binary hard-codes to "prod" and which
// is not readable from the environment at all (docs/ROADMAP.md). Modelling the
// name would cover exactly one of the suffix's sources and stay silently wrong
// for the rest — and a credential captured against one OAuth endpoint is not the
// same account universe as another, which kae's snapshots do not record.
const EnvCustomOAuthURL = "CLAUDE_CODE_CUSTOM_OAUTH_URL"

// fallbackKeychainAccount is the literal Claude Code uses when neither $USER
// nor the OS username is a usable account attribute.
const fallbackKeychainAccount = "claude-code-user"

// A **relative** CLAUDE_CONFIG_DIR resolves against whichever process reads it,
// and kae is invoked from anywhere in the project while claude runs from the
// user's shell. claude uses the value verbatim — at 2.1.220 the resolver is
// `(process.env.CLAUDE_CONFIG_DIR ?? join(homedir(), ".claude")).normalize("NFC")`,
// Unicode normalization and nothing else — so the two read different directories
// whenever their working directories differ. There is no default to fall back to
// while the variable is set, so kae keeps the value and warns.
//
// Half of it is safe, and saying so is what keeps the warning honest: the
// keychain service hashes that same raw string, so both processes resolve the
// **same item**. What diverges is every file artifact — the identity cache, and
// the credential itself under the file driver.
const relativeConfigDirWarning = "CLAUDE_CONFIG_DIR is relative: claude resolves it against its own" +
	" working directory, so kae writes the identity cache (and, under the file driver, the" +
	" credential) where claude does not read it — set an absolute path. The keychain item is" +
	" unaffected: its service name hashes the variable's raw value, not a resolved path."

// relativeConfigDirWarnings is the Detect/Doctor payload for a relative
// CLAUDE_CONFIG_DIR: one warning, or none. Both surfaces read it so neither
// can drift.
func relativeConfigDirWarnings(env adapter.Env) []string {
	return adapter.RelativeEnvWarning(env, "CLAUDE_CONFIG_DIR", relativeConfigDirWarning)
}

// keychainAccountPattern is the validation Claude Code applies to the account
// attribute before using it.
var keychainAccountPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// keychainService returns the Keychain service name Claude Code resolves for
// this environment. The name is a rule, not a constant, and kae's own isolation
// modes are what trigger the interesting branch:
//
//	CLAUDE_CONFIG_DIR unset  ->  "Claude Code-credentials"
//	CLAUDE_CONFIG_DIR set    ->  "Claude Code-credentials-<sha8>"
//
// where <sha8> is the first 8 hex characters of sha256 over the env string,
// NFC-normalized. There is no path resolution or cleaning in that hash, so a
// trailing slash is a different item — callers must hash exactly the string they
// put in the environment, which is why kae hashes the directory it writes into
// the mise fragment rather than a re-derived path.
//
// The NFC step is not cosmetic on macOS: a decomposed path (a home or
// XDG_DATA_HOME carrying non-ASCII characters, which the filesystem may hand
// back in NFD) would hash differently here than in claude, and kae would write an
// item claude never reads — the very failure this function exists to prevent.
// Normalization applies to the hash input only; the path itself stays byte-exact
// so it still resolves on disk, which is what claude does too.
//
// Modelling this as a constant is what made a pinned directory silently drift out
// of kae's control: kae wrote `<pinDir>/.credentials.json`, claude read the
// per-directory keychain item instead (reads are keychain-first and its first
// token refresh creates that item and deletes the file), and every offline guard
// stayed green while the session ran the previous account. docs/VALIDATION.md
// § "Upstream Behaviour Assumptions" carries the rule and a login-free procedure
// for re-verifying it.
//
// Deliberately not modelled: the build's OAuth suffix, which sits between
// "Claude Code" and "-credentials" and is empty only for the production build
// (docs/ROADMAP.md).
// dirScoped reports whether the returned name is namespaced by the config dir,
// which is what makes the item safe to write for one bound directory.
func keychainService(env adapter.Env) (name string, dirScoped bool) {
	dir := env.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		return KeychainService, false
	}
	sum := sha256.Sum256([]byte(norm.NFC.String(dir)))
	return fmt.Sprintf("%s-%x", KeychainService, sum[:4]), true
}

// keychainAccount returns the account attribute Claude Code uses for its
// keychain item: $USER, or the OS username when that is unset, and the
// fallback literal when the result is not a valid attribute.
//
// It has to match, not merely be plausible: claude's reads are account-scoped
// (`find-generic-password -a <account> -s <service>`), so an item kae creates
// under a different account attribute is invisible to claude even when the
// service name is right — the same silent wrong-credential outcome as a wrong
// service name.
func keychainAccount(env adapter.Env) string {
	name := env.Getenv("USER")
	if name == "" {
		name = env.Username
	}
	if !keychainAccountPattern.MatchString(name) {
		return fallbackKeychainAccount
	}
	return name
}

// envConflicts override subscription login inside Claude Code.
//
// The host-managed entries are a third credential source, measured on 2.1.220:
// with CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST truthy, claude reads the JSON file
// CLAUDE_CODE_HOST_CREDS_FILE names and injects its token into the variable
// CLAUDE_CODE_HOST_AUTH_ENV_VAR names — default ANTHROPIC_AUTH_TOKEN, but the
// host may name any variable, which is why kae warns on the mechanism rather than
// on the destination a static list cannot know. ANTHROPIC_UNIX_SOCKET is the same
// predicate's third arm (requests go to a host socket instead). None of these
// moves kae's stores, so they warn like the token variables rather than making the
// tool unsupported the way EnvCustomOAuthURL does.
var envConflicts = []string{
	"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN",
	"ANTHROPIC_UNIX_SOCKET", "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST",
	"CLAUDE_CODE_HOST_CREDS_FILE", "CLAUDE_CODE_HOST_AUTH_ENV_VAR",
}

type Claude struct{}

func init() { adapter.Register(Claude{}) }

func (Claude) ID() string { return constants.ToolClaude }

func (Claude) Binary() string { return "claude" }

// VerifiedVersion is the Claude Code release kae's behaviour assumptions were
// last checked on (docs/VALIDATION.md "Upstream Behaviour Assumptions"). 2.1.220
// is where /oauthAccount's self-heal was measured as gated behind a 24h
// profileFetchedAt TTL that a token refresh keeps renewing — the finding kae's
// identity switch depends on — where the credential's *storage resolution* was
// measured (the keychain service name, its per-config-dir suffix, and the account
// attribute that keychainService and keychainAccount reproduce), and where the
// refresh token was measured to **rotate single-use** (that row states what it means
// for kae; it is not repeated here). Several assumptions
// now hang on a version whose only offline signal is this string — the table is
// the count, not this comment — so a newer minor is worth re-measuring, and none
// of those procedures needs a login against an account in use.
func (Claude) VerifiedVersion() string { return "2.1.220" }

// VerifiedOn is when those assumptions were last checked (docs/VALIDATION.md).
func (Claude) VerifiedOn() string { return "2026-08-04" }

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
	// CLAUDE_SECURESTORAGE_CONFIG_DIR moves *both* stores at once: it becomes the
	// hash input for the keychain service name and the directory holding
	// .credentials.json, displacing CLAUDE_CONFIG_DIR for both. Set to the empty
	// string it goes further and removes the per-directory suffix entirely,
	// collapsing every config dir onto one shared item — which would silently
	// destroy the isolation `kae pin` exists to provide. kae has no way to keep
	// per-directory bindings honest under any of those, so refuse the tool rather
	// than write a credential nothing reads. Checked before the driver override
	// because it invalidates the file location too, not just the keychain one.
	if env.IsSet(EnvSecureStorageDir) {
		return "", fmt.Errorf(
			"%w: %s is set, which moves claude's credential store outside what kae can model"+
				" (unset it to let kae manage claude)",
			adapter.ErrUnsupported, EnvSecureStorageDir,
		)
	}
	// CLAUDE_CODE_CUSTOM_OAUTH_URL renames both stores through the build's OAuth
	// suffix — see EnvCustomOAuthURL. Checked before the driver override because the
	// override only redirects the credential to a file, while the suffix also moves
	// the identity file the oauth_account artifact patches.
	if env.Getenv(EnvCustomOAuthURL) != "" {
		return "", fmt.Errorf(
			"%w: %s is set, which renames claude's keychain item and identity file"+
				" (unset it to let kae manage claude)",
			adapter.ErrUnsupported, EnvCustomOAuthURL,
		)
	}
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
// specs[0] as the credential (the per-directory materializer resolves it by name
// through credentialArtifactName instead), so the order is a contract of this
// adapter, pinned by TestClaudeArtifactsLinux/Darwin.
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
		service, dirScoped := keychainService(env)
		credential = artifact.Spec{
			Name:            "claude_ai_oauth",
			Kind:            constants.KindKeychain,
			Target:          service,
			Pointer:         "/claudeAiOauth",
			KeychainAccount: keychainAccount(env),
			// The account is a rule claude applies to every read
			// (`find-generic-password -a <account> -s <service>`), so kae must
			// scope to it rather than take the service's first item: an item left
			// under a former $USER is one claude cannot see, and reusing its
			// account would keep kae writing where claude never looks.
			KeychainMatchAccount: true,
			KeychainDirBindable:  dirScoped,
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
	info.Warnings = append(info.Warnings, adapter.EnvConflictWarnings(env, envConflicts)...)
	info.Warnings = append(info.Warnings, relativeConfigDirWarnings(env)...)
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
// refreshTokenExpiresAt is persisted next to expiresAt and is the **login's own
// deadline**, not a second rolling token: expiresAt moves forward on every refresh,
// this one is set when `/login` runs and stays put. Claude Code warns on exactly it
// ("Your login expires in N days · run /login to renew"). It matters because without
// it an expired access token plus any refresh string reads as "recoverable" long
// after the login is not. The lifetime, upstream's warning threshold and its version
// history, and how to measure either are in the claude row of docs/VALIDATION.md.
//
// The tombstone is the other half. When a refresh fails with invalid_grant,
// Claude Code overwrites the credential in place with blank tokens and
// expiresAt 0 — an explicit death certificate. Read literally that is "no expiry
// recorded", the most harmless state kae has, so it is translated here (the one
// place that knows this payload) into Revoked.
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
		Revoked: !hasAccess && !hasRefresh,
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
	checks = append(checks, adapter.EnvConflictChecksFrom(tool, relativeConfigDirWarnings(env))...)
	// The macOS driver is keychain-based, but a stray plaintext credential
	// file with loose permissions deserves the warning there too.
	if check, ok := adapter.FileModeCheck(env, tool, credentialsPath(env)); ok {
		checks = append(checks, check)
	}
	return checks
}
