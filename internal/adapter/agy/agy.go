// Package agy implements the experimental Antigravity CLI adapter.
//
// Antigravity keeps its CLI state under ~/.gemini/antigravity-cli/ and
// stores credentials in the OS keyring when available, falling back to a
// credential file (observed on WSL/headless setups). The keyring item
// contract is undocumented, so this adapter supports only the file-based
// storage: each known credential filename is one whole-file artifact, and
// doctor warns when the keyring is likely in use. See docs/ADAPTERS.md.
package agy

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// credentialFiles are the file-based credential names observed across agy
// versions. All are captured/applied so version changes round-trip.
var credentialFiles = []string{"credentials.enc", "credentials.json", "oauth_creds.json"}

type Agy struct{}

func init() { adapter.Register(Agy{}) }

func (Agy) ID() string { return constants.ToolAgy }

func (Agy) Binary() string { return "agy" }

func cliDir(env adapter.Env) string {
	return filepath.Join(env.Home, ".gemini", "antigravity-cli")
}

func artifactName(file string) string {
	return strings.ReplaceAll(file, ".", "_")
}

func (Agy) Artifacts(_ context.Context, env adapter.Env) ([]artifact.Spec, error) {
	specs := make([]artifact.Spec, 0, len(credentialFiles))
	for _, file := range credentialFiles {
		specs = append(specs, artifact.Spec{
			Name:   artifactName(file),
			Kind:   constants.KindFile,
			Target: filepath.Join(cliDir(env), file),
		})
	}
	return specs, nil
}

// authFilePresent reports whether any file-based credential exists.
func authFilePresent(env adapter.Env) bool {
	for _, file := range credentialFiles {
		if _, err := os.Stat(filepath.Join(cliDir(env), file)); err == nil {
			return true
		}
	}
	return false
}

func (a Agy) Detect(_ context.Context, env adapter.Env) (adapter.Info, error) {
	info := adapter.Info{
		Tool:     constants.ToolAgy,
		Driver:   constants.DriverAgyFileSnapshot,
		Warnings: []string{"agy adapter is experimental (file-based credential storage only)"},
	}
	if _, err := env.LookPath("agy"); err == nil {
		info.BinaryPresent = true
	}
	info.AuthPresent = authFilePresent(env)
	if !info.AuthPresent {
		if _, err := os.Stat(cliDir(env)); err == nil {
			info.Warnings = append(info.Warnings,
				"no credential file found; agy likely uses the OS keyring on this platform, which kae cannot switch yet")
		}
	}
	return info, nil
}

func (a Agy) Doctor(ctx context.Context, env adapter.Env) []adapter.Check {
	tool := constants.ToolAgy
	checks := []adapter.Check{adapter.BinaryCheck(env, tool, "agy")}
	info, _ := a.Detect(ctx, env)
	switch {
	case info.AuthPresent:
		checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckAuthPresent,
			Status: constants.StatusOK, Message: "file-based credential found"})
	default:
		message := "no file-based credential under ~/.gemini/antigravity-cli/"
		if _, err := os.Stat(cliDir(env)); err == nil {
			message += " (agy likely uses the OS keyring, which kae cannot switch yet)"
		} else {
			message += " (agy has not been set up on this machine)"
		}
		checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckAuthPresent,
			Status: constants.StatusWarn, Message: message})
	}
	checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckDriver,
		Status:  constants.StatusWarn,
		Message: "driver: " + constants.DriverAgyFileSnapshot + " (experimental)"})
	return checks
}
