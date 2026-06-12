// Package opencode implements the OpenCode adapter. Auth mode patches only
// the /openai (ChatGPT subscription) entry of the XDG-data auth.json;
// every other key in that file is an independent provider credential
// (API-key territory, handled by env mode) and is preserved. See
// docs/ADAPTERS.md.
package opencode

import (
	"context"
	"os"
	"path/filepath"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// openaiPointer selects the ChatGPT-subscription credential inside
// auth.json. The whole file is never replaced: sibling keys are other
// providers' credentials that must survive an account switch.
const openaiPointer = "/openai"

type Opencode struct{}

func init() { adapter.Register(Opencode{}) }

func (Opencode) ID() string { return constants.ToolOpencode }

// dataDir resolves opencode's XDG data directory, honoring XDG_DATA_HOME
// as the live base path when already set.
func dataDir(env adapter.Env) string {
	if dir := env.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "opencode")
	}
	return filepath.Join(env.Home, ".local", "share", "opencode")
}

func authJSONPath(env adapter.Env) string { return filepath.Join(dataDir(env), "auth.json") }

func (o Opencode) Artifacts(_ context.Context, env adapter.Env) ([]artifact.Spec, error) {
	return []artifact.Spec{{
		Name:    "openai_auth",
		Kind:    constants.KindJSONPointer,
		Target:  authJSONPath(env),
		Pointer: openaiPointer,
	}}, nil
}

func (o Opencode) Detect(ctx context.Context, env adapter.Env) (adapter.Info, error) {
	info := adapter.Info{Tool: constants.ToolOpencode, Driver: constants.DriverOpencodeFilePatch, Warnings: []string{}}
	if _, err := env.LookPath("opencode"); err == nil {
		info.BinaryPresent = true
	}
	specs, err := o.Artifacts(ctx, env)
	if err != nil {
		return info, err
	}
	v, err := artifact.ReadLive(ctx, specs[0])
	if err != nil {
		return info, err
	}
	info.AuthPresent = v.Present
	if !v.Present {
		if _, statErr := os.Stat(authJSONPath(env)); statErr == nil {
			info.Warnings = append(info.Warnings,
				"auth.json has no openai entry; only the ChatGPT subscription login is switched (API-key providers belong to env mode)")
		}
	}
	return info, nil
}

func (o Opencode) Doctor(ctx context.Context, env adapter.Env) []adapter.Check {
	tool := constants.ToolOpencode
	checks := []adapter.Check{adapter.BinaryCheck(env, tool, "opencode")}
	info, err := o.Detect(ctx, env)
	switch {
	case err != nil:
		checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckAuthPresent,
			Status: constants.StatusError, Message: err.Error()})
	case info.AuthPresent:
		checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckAuthPresent,
			Status: constants.StatusOK, Message: "openai (ChatGPT subscription) login found in auth.json"})
	default:
		checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckAuthPresent,
			Status: constants.StatusWarn,
			Message: "no openai (ChatGPT subscription) login in auth.json; log in with `opencode auth login` first"})
	}
	checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckDriver,
		Status: constants.StatusOK, Message: "driver: " + constants.DriverOpencodeFilePatch})
	if env.GOOS != "windows" {
		if fileInfo, err := os.Stat(authJSONPath(env)); err == nil && fileInfo.Mode().Perm()&0o077 != 0 {
			checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckFileMode,
				Status: constants.StatusWarn,
				Message: authJSONPath(env) + " is group/world readable; expected 0600"})
		}
	}
	return checks
}
