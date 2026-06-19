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

	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/freshness"
)

// ErrUnsupported means the tool/platform combination has no auth driver;
// callers map it to exit code 5.
var ErrUnsupported = errors.New("unsupported")

// Env is the injected view of the live environment.
type Env struct {
	GOOS     string
	Home     string
	Getenv   func(string) string
	LookPath func(string) (string, error)
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

// EnvConflictChecks warns for each set environment variable that overrides
// the subscription login kae switches.
func EnvConflictChecks(env Env, tool string, vars []string) []Check {
	checks := []Check{}
	for _, name := range vars {
		if env.Getenv(name) != "" {
			checks = append(checks, Check{
				Tool: tool, Code: constants.CheckEnvConflict,
				Status:  constants.StatusWarn,
				Message: name + " is set and overrides the switched login",
			})
		}
	}
	return checks
}
