// Package adapter defines the per-tool boundary. Adapters declare which auth
// artifacts a tool has on the current platform and report health checks;
// all IO goes through internal/artifact and lower seams. The normative
// switched/preserved contract is docs/ADAPTERS.md.
package adapter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/freshness"
)

// ErrUnsupported means the tool/platform combination has no auth driver;
// callers map it to exit code 5.
var ErrUnsupported = errors.New("unsupported")

// Env is the injected view of the live environment.
type Env struct {
	GOOS   string
	Home   string
	Getenv func(string) string
	// Username is the OS account name, used only as the fallback a tool itself
	// falls back to when $USER is unset (claude names its keychain item
	// `$USER || os.userInfo().username`). Reading it from the environment view
	// instead of os/user keeps adapters injectable; empty is allowed and each
	// adapter decides what to do with it.
	Username string
	// LookupEnv reports a variable's value together with whether it is set at
	// all, for the rare case where an explicitly empty value means something
	// different from an absent one (claude's CLAUDE_SECURESTORAGE_CONFIG_DIR
	// set to "" disables its keychain namespacing, while unset does not).
	// Optional: use Env.IsSet, which degrades to Getenv when this is nil.
	LookupEnv func(string) (string, bool)
	LookPath  func(string) (string, error)
}

// IsSet reports whether key is present in the environment, including when its
// value is the empty string. Without an injected LookupEnv it degrades to a
// non-empty test, which cannot see an explicitly-emptied variable — so an
// adapter that refuses on IsSet stays correct in the common case and merely
// under-refuses in a test env that did not inject one.
//
// Not subject to the global-scope masking that wraps Getenv (cmd.applyGlobalScope
// hides kae-managed isolation values): that masking covers only variables kae
// itself sets, and every variable reached through IsSet is user-set by
// definition.
func (e Env) IsSet(key string) bool {
	if e.LookupEnv != nil {
		_, ok := e.LookupEnv(key)
		return ok
	}
	return e.Getenv(key) != ""
}

// Info is the result of detecting a tool's live state.
type Info struct {
	Tool          string
	Driver        string
	BinaryPresent bool
	AuthPresent   bool
	Warnings      []string
}

// Check is one doctor finding.
type Check struct {
	Tool    string `json:"tool"`
	Code    string `json:"code"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// Adapter is implemented once per tool.
type Adapter interface {
	ID() string
	// Binary is the tool's CLI executable name. It usually equals ID(), but
	// not always (cursor's id is "cursor", its binary is "cursor-agent"); it
	// is the single source of truth for LookPath probes, the login command,
	// and the generated mise run tasks.
	Binary() string
	// Detect inspects the live environment: binary, driver, auth presence.
	Detect(ctx context.Context, env Env) (Info, error)
	// Artifacts returns the auth artifact specs for this platform, or
	// ErrUnsupported / artifact.ErrUnsafe-wrapped refusals.
	Artifacts(ctx context.Context, env Env) ([]artifact.Spec, error)
	// Doctor returns adapter-specific health checks.
	Doctor(ctx context.Context, env Env) []Check
	// VerifiedVersion is the upstream release kae's behaviour assumptions for
	// this tool were last verified against, so doctor can say when the installed
	// tool has moved past it (docs/VALIDATION.md § "Upstream Behaviour
	// Assumptions"). kae depends on undocumented upstream layout *and*
	// behaviour, and a behaviour-only change passes every structure guard, so the
	// version is the only signal available offline. It is a method of Adapter
	// rather than an optional interface because every tool owes one — unlike
	// Fresher/Identifier this is not capability-dependent.
	//
	// "" means "no usable signal": doctor skips the tool. Only a tool whose
	// version scheme the comparison cannot read may return it (cursor's date
	// versions), and the reason belongs in that adapter's doc comment.
	VerifiedVersion() string
}

// Identifier is implemented by adapters that can read the live login identity
// (an email address or account handle) so `kae add <tool>` with no account name
// can derive a default. The returned identity is raw — the caller sanitizes it
// into an account name. Every current tool adapter implements it; one without it
// would require an explicit account name (the cmd path is capability-based, not
// per-tool). A detection failure (logged out, unreadable) returns an error so
// the caller names the explicit form rather than silently falling back.
type Identifier interface {
	Identity(ctx context.Context, env Env) (string, error)
}

// Fresher is implemented by adapters whose switched credential carries a
// readable expiry / refresh-token (claude/codex/opencode/cursor). It turns a
// captured payload into a freshness.Info using the primitives in
// internal/freshness, so per-tool credential knowledge lives on the adapter
// (the registry), not in a central switch. cmd dispatches to it for the
// switch-time stale warning and doctor credential-health; a tool with no
// Fresher (copilot pointer, agy blob) is treated as not-datable (Known=false).
type Fresher interface {
	Freshness(payload []byte) freshness.Info
}

var registry = map[string]Adapter{}

// Register installs an adapter; called from tool packages' init via Install.
func Register(a Adapter) { registry[a.ID()] = a }

// ForTool returns the adapter for a tool id.
func ForTool(id string) (Adapter, error) {
	a, ok := registry[id]
	if !ok {
		return nil, fmt.Errorf("no adapter for tool %q", id)
	}
	return a, nil
}

// BinaryCheck is a shared doctor helper for upstream CLI presence.
func BinaryCheck(env Env, tool, binary string) Check {
	if _, err := env.LookPath(binary); err != nil {
		return Check{
			Tool: tool, Code: constants.CheckBinaryPresent, Status: constants.StatusWarn,
			Message: binary + " not found in PATH",
		}
	}
	return Check{
		Tool: tool, Code: constants.CheckBinaryPresent, Status: constants.StatusOK,
		Message: binary + " found in PATH",
	}
}

// FileModeCheck warns when a live credential file is group/world readable.
// ok=false means no finding: a missing file, or windows, where POSIX
// permission bits are meaningless.
func FileModeCheck(env Env, tool, path string) (Check, bool) {
	if env.GOOS == "windows" {
		return Check{}, false
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0o077 == 0 {
		return Check{}, false
	}
	return Check{
		Tool: tool, Code: constants.CheckFileMode, Status: constants.StatusWarn,
		Message: path + " is group/world readable; expected 0600",
	}, true
}

// EnvConflictWarning is the message for one environment variable that overrides
// the subscription login kae switches. Detect reports it in Info.Warnings and
// Doctor in a Check, so the wording lives here instead of once per surface.
func EnvConflictWarning(name string) string {
	return name + " is set and overrides the switched login"
}

// EnvConflictWarnings returns one EnvConflictWarning per set variable, for an
// adapter's Detect.
func EnvConflictWarnings(env Env, vars []string) []string {
	warnings := []string{}
	for _, name := range vars {
		if env.Getenv(name) != "" {
			warnings = append(warnings, EnvConflictWarning(name))
		}
	}
	return warnings
}

// IsRelativeEnv reports whether a path variable is set to a relative value —
// the case where kae and the tool resolve one variable against two different
// working directories and therefore two different files. Every tool measured so
// far uses such a variable verbatim, without the absolute-path check the XDG spec
// asks for, so the divergence is real and the adapter's job is to warn about it
// (docs/ADAPTERS.md; the per-variable wording stays with the adapter because the
// consequence differs per tool).
func IsRelativeEnv(env Env, name string) bool {
	value := env.Getenv(name)
	return value != "" && !filepath.IsAbs(value)
}

// EnvConflictChecksFrom wraps already-built warnings as env_conflict checks, so
// the Doctor half of an environment warning is assembled in one place. Adapters
// whose warning is not a per-variable "overrides the login" message (a relative
// path variable, a store the tool may bypass) reach it directly with their own
// text; EnvConflictChecks is the same thing for a plain variable list.
func EnvConflictChecksFrom(tool string, warnings []string) []Check {
	checks := make([]Check, 0, len(warnings))
	for _, warning := range warnings {
		checks = append(checks, Check{
			Tool: tool, Code: constants.CheckEnvConflict,
			Status: constants.StatusWarn, Message: warning,
		})
	}
	return checks
}

// EnvConflictChecks warns for each set environment variable that overrides
// the subscription login kae switches.
func EnvConflictChecks(env Env, tool string, vars []string) []Check {
	return EnvConflictChecksFrom(tool, EnvConflictWarnings(env, vars))
}
