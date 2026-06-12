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
	// Detect inspects the live environment: binary, driver, auth presence.
	Detect(ctx context.Context, env Env) (Info, error)
	// Artifacts returns the auth artifact specs for this platform, or
	// ErrUnsupported / artifact.ErrUnsafe-wrapped refusals.
	Artifacts(ctx context.Context, env Env) ([]artifact.Spec, error)
	// Doctor returns adapter-specific health checks.
	Doctor(ctx context.Context, env Env) []Check
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
		return Check{Tool: tool, Code: constants.CheckBinaryPresent, Status: constants.StatusWarn,
			Message: binary + " not found in PATH"}
	}
	return Check{Tool: tool, Code: constants.CheckBinaryPresent, Status: constants.StatusOK,
		Message: binary + " found in PATH"}
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
	return Check{Tool: tool, Code: constants.CheckFileMode, Status: constants.StatusWarn,
		Message: path + " is group/world readable; expected 0600"}, true
}

// EnvConflictChecks warns for each set environment variable that overrides
// the subscription login kae switches.
func EnvConflictChecks(env Env, tool string, vars []string) []Check {
	checks := []Check{}
	for _, name := range vars {
		if env.Getenv(name) != "" {
			checks = append(checks, Check{Tool: tool, Code: constants.CheckEnvConflict,
				Status: constants.StatusWarn,
				Message: name + " is set and overrides the switched login"})
		}
	}
	return checks
}
