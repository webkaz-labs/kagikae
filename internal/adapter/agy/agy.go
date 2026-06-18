// Package agy implements the Antigravity CLI adapter.
//
// On macOS, agy stores its credential in the login Keychain under service
// "gemini", account "antigravity" (discovery 2026-06-18; docs/ADAPTERS.md):
// the payload is a single opaque ~686-byte token, captured and applied
// verbatim. The gemini service is shared with the Gemini ecosystem, so kae
// matches by service AND account — only acct=antigravity is agy's — and never
// touches a sibling gemini item. On Linux/WSL headless setups the credential is
// a file under ~/.gemini/antigravity-cli/, so the adapter keeps the file-based
// driver there. agy has no kae-drivable login (GUI/browser OAuth, no
// login/auth/whoami subcommand), so `kae add agy` is --no-login only.
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

// KeychainService and KeychainAccount identify agy's macOS Keychain item. The
// account is a fixed literal that disambiguates the shared gemini service
// (discovery 2026-06-18); it is not a per-login opaque id like codex's.
const (
	KeychainService = "gemini"
	KeychainAccount = "antigravity"
)

// noKeychainItemMsg is the logged-out message shared by Detect's warning and
// Doctor's warn check so the two cannot drift.
const noKeychainItemMsg = "no gemini/antigravity keychain item; log in with the Antigravity app first"

// credentialFiles are the file-based credential names observed across agy
// versions (Linux/WSL). All are captured/applied so version changes round-trip.
var credentialFiles = []string{"credentials.enc", "credentials.json", "oauth_creds.json"}

type Agy struct{}

func init() { adapter.Register(Agy{}) }

func (Agy) ID() string { return constants.ToolAgy }

func (Agy) Binary() string { return "agy" }

// keychainDriver reports whether this platform uses the macOS Keychain item
// (darwin) or the file-based snapshot (Linux/WSL headless).
func keychainDriver(env adapter.Env) bool { return env.GOOS == "darwin" }

func cliDir(env adapter.Env) string {
	return filepath.Join(env.Home, ".gemini", "antigravity-cli")
}

func artifactName(file string) string {
	return strings.ReplaceAll(file, ".", "_")
}

func (Agy) Artifacts(_ context.Context, env adapter.Env) ([]artifact.Spec, error) {
	if keychainDriver(env) {
		// One opaque ~686B token under gemini/antigravity. Pointer "" marks it
		// opaque (the guard is non-empty single-line, no JSON parse);
		// KeychainMatchAccount scopes every read/write/delete to the antigravity
		// account so the shared gemini service's sibling items are never touched.
		return []artifact.Spec{{
			Name:                 "credential",
			Kind:                 constants.KindKeychain,
			Target:               KeychainService,
			Pointer:              "",
			KeychainAccount:      KeychainAccount,
			KeychainMatchAccount: true,
		}}, nil
	}
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

func (a Agy) Detect(ctx context.Context, env adapter.Env) (adapter.Info, error) {
	info := adapter.Info{Tool: constants.ToolAgy, Warnings: []string{}}
	if _, err := env.LookPath("agy"); err == nil {
		info.BinaryPresent = true
	}
	if keychainDriver(env) {
		info.Driver = constants.DriverAgyKeychain
		specs, err := a.Artifacts(ctx, env)
		if err != nil {
			return info, err
		}
		v, err := artifact.ReadLive(ctx, specs[0])
		if err != nil {
			return info, err
		}
		info.AuthPresent = v.Present
		if !v.Present {
			info.Warnings = append(info.Warnings, noKeychainItemMsg)
		}
		return info, nil
	}
	info.Driver = constants.DriverAgyFileSnapshot
	info.Warnings = append(info.Warnings, "agy adapter on this platform uses file-based credential storage only")
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
	info, err := a.Detect(ctx, env)
	if keychainDriver(env) {
		switch {
		case err != nil:
			checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckAuthPresent,
				Status: constants.StatusError, Message: err.Error()})
		case info.AuthPresent:
			checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckAuthPresent,
				Status: constants.StatusOK, Message: "gemini/antigravity keychain item found"})
		default:
			checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckAuthPresent,
				Status: constants.StatusWarn, Message: noKeychainItemMsg})
		}
		checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckDriver,
			Status: constants.StatusOK, Message: "driver: " + constants.DriverAgyKeychain})
		return checks
	}
	switch {
	case info.AuthPresent:
		checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckAuthPresent,
			Status: constants.StatusOK, Message: "file-based credential found"})
	default:
		message := "no file-based credential under ~/.gemini/antigravity-cli/"
		if _, err := os.Stat(cliDir(env)); err == nil {
			message += " (agy likely uses the OS keyring, which kae cannot switch on this platform)"
		} else {
			message += " (agy has not been set up on this machine)"
		}
		checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckAuthPresent,
			Status: constants.StatusWarn, Message: message})
	}
	checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckDriver,
		Status:  constants.StatusWarn,
		Message: "driver: " + constants.DriverAgyFileSnapshot + " (file-based; keyring switching unsupported on this platform)"})
	return checks
}
