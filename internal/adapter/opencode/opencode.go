// Package opencode implements the OpenCode adapter. Auth mode patches only
// the /openai (ChatGPT subscription) entry of the XDG-data auth.json;
// every other key in that file is an independent provider credential
// (API-key territory, handled by env mode) and is preserved. See
// docs/ADAPTERS.md.
package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/freshness"
	"github.com/webkaz-labs/kagikae/internal/paths"
)

// openaiPointer selects the ChatGPT-subscription credential inside
// auth.json. The whole file is never replaced: sibling keys are other
// providers' credentials that must survive an account switch.
const openaiPointer = "/openai"

type Opencode struct{}

func init() { adapter.Register(Opencode{}) }

func (Opencode) ID() string { return constants.ToolOpencode }

func (Opencode) Binary() string { return "opencode" }

// authJSONPath resolves opencode's credential file, honoring XDG_DATA_HOME
// as the live base path when already set (absolute values only, as
// everywhere in kae).
func authJSONPath(env adapter.Env) string {
	return filepath.Join(paths.XDGDataHome(env.Getenv, env.Home, "opencode"), "auth.json")
}

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
		// ReadLive cannot distinguish a missing file from a file without
		// the openai key, and only the latter deserves an explanation.
		if _, statErr := os.Stat(specs[0].Target); statErr == nil {
			info.Warnings = append(info.Warnings,
				"auth.json has no openai entry; only the ChatGPT subscription login is switched (API-key providers belong to env mode)")
		}
	}
	return info, nil
}

// Identity reads the ChatGPT-subscription accountId from auth.json's /openai
// entry so `kae add opencode` (no name) can default the account name. Only the
// openai entry is consulted — API-key providers belong to env mode.
func (o Opencode) Identity(_ context.Context, env adapter.Env) (string, error) {
	path := authJSONPath(env)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	var doc struct {
		Openai struct {
			AccountID string `json:"accountId"`
		} `json:"openai"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	if doc.Openai.AccountID == "" {
		return "", fmt.Errorf("no openai.accountId in %s", path)
	}
	return doc.Openai.AccountID, nil
}

// Freshness reads the /openai sub-value {type, refresh, access, expires}.
func (Opencode) Freshness(payload []byte) freshness.Info {
	obj, ok := freshness.DecodeObject(payload)
	if !ok {
		return freshness.Info{}
	}
	return freshness.Info{
		Known:      true,
		ExpiresAt:  freshness.EpochToTime(freshness.NumberFrom(obj["expires"])),
		HasRefresh: freshness.NonEmptyString(obj["refresh"]),
	}
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
			Status:  constants.StatusWarn,
			Message: "no openai (ChatGPT subscription) login in auth.json; log in with `opencode auth login` first"})
	}
	checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckDriver,
		Status: constants.StatusOK, Message: "driver: " + constants.DriverOpencodeFilePatch})
	if check, ok := adapter.FileModeCheck(env, tool, authJSONPath(env)); ok {
		checks = append(checks, check)
	}
	return checks
}
