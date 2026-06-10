// Package gemini implements the Gemini CLI adapter. Auth mode swaps the
// Google-login OAuth cache (oauth_creds.json, google_accounts.json) and
// preserves settings; see docs/ADAPTERS.md.
package gemini

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// TransitionNotice surfaces the scheduled move of personal Gemini CLI
// serving to Antigravity CLI (2026-06-18).
const TransitionNotice = "personal Gemini CLI serving moves to Antigravity CLI on 2026-06-18; plan migration for Google AI Pro/Ultra accounts"

var envConflicts = []string{"GEMINI_API_KEY", "GOOGLE_APPLICATION_CREDENTIALS"}

type Gemini struct{}

func init() { adapter.Register(Gemini{}) }

func (Gemini) ID() string { return constants.ToolGemini }

func geminiDir(env adapter.Env) string { return filepath.Join(env.Home, ".gemini") }

func oauthCredsPath(env adapter.Env) string {
	return filepath.Join(geminiDir(env), "oauth_creds.json")
}

func (g Gemini) Artifacts(_ context.Context, env adapter.Env) ([]artifact.Spec, error) {
	return []artifact.Spec{
		{Name: "oauth_creds", Kind: constants.KindFile, Target: oauthCredsPath(env)},
		{Name: "google_accounts", Kind: constants.KindFile,
			Target: filepath.Join(geminiDir(env), "google_accounts.json")},
	}, nil
}

func (g Gemini) Detect(ctx context.Context, env adapter.Env) (adapter.Info, error) {
	info := adapter.Info{Tool: constants.ToolGemini, Driver: constants.DriverGeminiOAuthCache, Warnings: []string{}}
	if _, err := env.LookPath("gemini"); err == nil {
		info.BinaryPresent = true
	}
	if _, err := os.Stat(oauthCredsPath(env)); err == nil {
		info.AuthPresent = true
	}
	for _, name := range envConflicts {
		if env.Getenv(name) != "" {
			info.Warnings = append(info.Warnings, name+" is set and takes precedence over the OAuth cache")
		}
	}
	return info, nil
}

// selectedAuthType reads the configured auth type from settings.json for
// doctor reporting only; the file is never written.
func selectedAuthType(env adapter.Env) string {
	data, err := os.ReadFile(filepath.Join(geminiDir(env), "settings.json"))
	if err != nil {
		return ""
	}
	var settings struct {
		SelectedAuthType string `json:"selectedAuthType"`
		Security         struct {
			Auth struct {
				SelectedType string `json:"selectedType"`
			} `json:"auth"`
		} `json:"security"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return ""
	}
	if settings.SelectedAuthType != "" {
		return settings.SelectedAuthType
	}
	return settings.Security.Auth.SelectedType
}

func (g Gemini) Doctor(ctx context.Context, env adapter.Env) []adapter.Check {
	tool := constants.ToolGemini
	checks := []adapter.Check{adapter.BinaryCheck(env, tool, "gemini")}
	info, _ := g.Detect(ctx, env)
	if info.AuthPresent {
		checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckAuthPresent,
			Status: constants.StatusOK, Message: "Google OAuth cache found"})
	} else {
		checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckAuthPresent,
			Status: constants.StatusWarn, Message: "no Google OAuth cache (log in with gemini first)"})
	}
	if authType := selectedAuthType(env); authType != "" {
		status := constants.StatusOK
		if authType != "oauth-personal" {
			status = constants.StatusWarn
		}
		checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckCredentialStore,
			Status: status, Message: "configured auth type: " + authType})
	}
	checks = append(checks, adapter.EnvConflictChecks(env, tool, envConflicts)...)
	if env.WarnAntigravityTransition {
		checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckTransitionNotice,
			Status: constants.StatusWarn, Message: TransitionNotice})
	}
	return checks
}
