// Package copilot implements the GitHub Copilot CLI adapter. Unlike the other
// tools, copilot keeps each account's OAuth token in its own OS-keychain item
// (service copilot-cli, account <host>:<user>) and they coexist; "switching"
// means repointing the active account recorded in ~/.copilot/config.json, not
// swapping a credential. The adapter therefore patches only the config's
// /lastLoggedInUser pointer (a JSONC file — comments preserved), leaving the
// keychain tokens, loggedInUsers, and trustedFolders untouched. See
// docs/ADAPTERS.md.
package copilot

import (
	"context"
	"path/filepath"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

const (
	binaryName      = "copilot"
	lastUserPointer = "/lastLoggedInUser"
)

// envConflicts override the keychain login (login --help precedence order).
var envConflicts = []string{"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"}

type Copilot struct{}

func init() { adapter.Register(Copilot{}) }

func (Copilot) ID() string { return constants.ToolCopilot }

func (Copilot) Binary() string { return binaryName }

func configJSONPath(env adapter.Env) string {
	return filepath.Join(env.Home, ".copilot", "config.json")
}

func (c Copilot) Artifacts(_ context.Context, env adapter.Env) ([]artifact.Spec, error) {
	return []artifact.Spec{{
		Name:    "last_logged_in_user",
		Kind:    constants.KindJSONPointer,
		Target:  configJSONPath(env),
		Pointer: lastUserPointer,
		JSONC:   true, // ~/.copilot/config.json carries leading // comments
	}}, nil
}

func (c Copilot) Detect(ctx context.Context, env adapter.Env) (adapter.Info, error) {
	info := adapter.Info{Tool: constants.ToolCopilot, Driver: constants.DriverCopilotConfigPointer, Warnings: []string{}}
	if _, err := env.LookPath(binaryName); err == nil {
		info.BinaryPresent = true
	}
	for _, name := range envConflicts {
		if env.Getenv(name) != "" {
			info.Warnings = append(info.Warnings, name+" is set and overrides the switched login")
		}
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
	return info, nil
}

func (c Copilot) Doctor(ctx context.Context, env adapter.Env) []adapter.Check {
	tool := constants.ToolCopilot
	checks := []adapter.Check{adapter.BinaryCheck(env, tool, binaryName)}
	info, err := c.Detect(ctx, env)
	switch {
	case err != nil:
		checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckAuthPresent,
			Status: constants.StatusError, Message: err.Error()})
	case info.AuthPresent:
		checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckAuthPresent,
			Status: constants.StatusOK, Message: "active account recorded in config.json"})
	default:
		checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckAuthPresent,
			Status:  constants.StatusWarn,
			Message: "no active account in config.json; log in with `copilot login` first"})
	}
	checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckDriver,
		Status: constants.StatusOK, Message: "driver: " + constants.DriverCopilotConfigPointer})
	checks = append(checks, adapter.EnvConflictChecks(env, tool, envConflicts)...)
	if check, ok := adapter.FileModeCheck(env, tool, configJSONPath(env)); ok {
		checks = append(checks, check)
	}
	return checks
}
