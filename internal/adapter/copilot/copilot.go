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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/patch"
)

const (
	binaryName      = "copilot"
	lastUserPointer = "/lastLoggedInUser"

	// EnvHome relocates copilot's configuration directory. It replaces
	// ~/.copilot outright — the value is that directory, not a parent — and it is
	// copilot's own sanctioned mechanism: the deprecated `--config-dir` flag it
	// takes precedence over says "use COPILOT_HOME env var", and the flag's help
	// text describes the variable as "override the directory where configuration
	// and state files are stored".
	EnvHome = "COPILOT_HOME"
)

// envConflicts override the keychain login (login --help precedence order).
var envConflicts = []string{"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"}

// relativeHomeWarning is shared by Detect and Doctor so the two cannot drift.
//
// copilot applies no normalization to COPILOT_HOME, so a relative value resolves
// against **copilot's** working directory — and kae, invoked from anywhere in the
// project, resolves the same value against its own. Following it verbatim is still
// the closest kae can get (there is no default to fall back to while the variable
// is set, unlike opencode's XDG case), so kae keeps the value and warns: the two
// agree only while both run from the same directory.
const relativeHomeWarning = EnvHome + " is relative: copilot resolves it against its own working" +
	" directory, so kae only writes the file copilot reads while both run from the same" +
	" directory (set an absolute path)"

// relativeHomeWarnings is the Detect/Doctor payload for a relative COPILOT_HOME:
// one warning, or none. Both surfaces read it so neither can drift.
func relativeHomeWarnings(env adapter.Env) []string {
	return adapter.RelativeEnvWarning(env, EnvHome, relativeHomeWarning)
}

type Copilot struct{}

func init() { adapter.Register(Copilot{}) }

func (Copilot) ID() string { return constants.ToolCopilot }

func (Copilot) Binary() string { return binaryName }

// VerifiedVersion is the GitHub Copilot CLI release kae's behaviour assumptions
// were last checked on (docs/VALIDATION.md "Upstream Behaviour Assumptions").
func (Copilot) VerifiedVersion() string { return "1.0.79" }

// VerifiedOn is when those assumptions were last checked (docs/VALIDATION.md).
func (Copilot) VerifiedOn() string { return "2026-08-17" }

// configHome resolves the directory holding config.json. COPILOT_HOME is
// honored verbatim, the way copilot itself uses it (`t?.configDir ??
// process.env.COPILOT_HOME ?? join(homedir(), ".copilot")`, re-measured on 1.0.79):
// no normalization, no absolute-path check. Modelling it as $HOME/.copilot made
// every switch in a directory with COPILOT_HOME set patch a config.json copilot
// never reads.
func configHome(env adapter.Env) string {
	if dir := env.Getenv(EnvHome); dir != "" {
		return dir
	}
	return filepath.Join(env.Home, ".copilot")
}

// configJSONPath is the config file copilot's own login was measured writing
// (`writeKey("lastLoggedInUser", ...)` into config.json, read from the 1.0.61
// bundle; not re-established on 1.0.79, where that key name is no longer in
// app.js). The settings-migration loader additionally falls back to a bare
// `config` in the same directory when config.json is absent, and no auth path was
// seen writing there, so kae targets config.json as an upstream login did.
func configJSONPath(env adapter.Env) string {
	return filepath.Join(configHome(env), "config.json")
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
	info.Warnings = append(info.Warnings, adapter.EnvConflictWarnings(env, envConflicts)...)
	info.Warnings = append(info.Warnings, relativeHomeWarnings(env)...)
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

// Identity reads /lastLoggedInUser.login from the JSONC config.json so
// `kae add copilot` (no name) can default the account name to the active login
// handle. The file carries leading // comments, so it is read as JSONC.
func (c Copilot) Identity(_ context.Context, env adapter.Env) (string, error) {
	path := configJSONPath(env)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	raw, found, err := patch.GetPointerJSONC(data, lastUserPointer)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	if !found {
		return "", fmt.Errorf("no %s in %s", lastUserPointer, path)
	}
	var user struct {
		Login string `json:"login"`
	}
	if err := json.Unmarshal(raw, &user); err != nil {
		return "", fmt.Errorf("parse %s%s: %w", path, lastUserPointer, err)
	}
	if user.Login == "" {
		return "", fmt.Errorf("no %s/login in %s", lastUserPointer, path)
	}
	return user.Login, nil
}

func (c Copilot) Doctor(ctx context.Context, env adapter.Env) []adapter.Check {
	tool := constants.ToolCopilot
	checks := []adapter.Check{adapter.BinaryCheck(env, tool, binaryName)}
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
			Status: constants.StatusOK, Message: "active account recorded in config.json",
		})
	default:
		checks = append(checks, adapter.Check{
			Tool: tool, Code: constants.CheckAuthPresent,
			Status:  constants.StatusWarn,
			Message: "no active account in config.json; log in with `copilot login` first",
		})
	}
	checks = append(checks, adapter.Check{
		Tool: tool, Code: constants.CheckDriver,
		Status: constants.StatusOK, Message: "driver: " + constants.DriverCopilotConfigPointer,
	})
	checks = append(checks, adapter.EnvConflictChecks(env, tool, envConflicts)...)
	checks = append(checks, adapter.EnvConflictChecksFrom(tool, relativeHomeWarnings(env))...)
	if check, ok := adapter.FileModeCheck(env, tool, configJSONPath(env)); ok {
		checks = append(checks, check)
	}
	return checks
}
