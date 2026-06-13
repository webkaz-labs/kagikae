package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/patch"
	"github.com/webkaz-labs/kagikae/internal/paths"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

// CmdAs swaps the credential of a bonded or pinned directory to a different
// account without changing the sharing set:
//
//	kae as <tool> <account>
//
// Valid only inside a directory bound with `kae bond` or `kae pin`. For bond
// mode, the bond dir is account-agnostic so the credential is overwritten in
// place. For pin mode, the config dir is re-keyed to the new account and the
// .mise.toml env entry is updated. Sessions and settings are never disturbed.
func CmdAs(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	opts, ok := parseCommon("as", flags, false, nil)
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 2 {
		return usageError("usage: %s as <tool> <account>", toolName)
	}
	tool, accountName := positionals[0], positionals[1]
	if err := validateToolAccount(tool, accountName, "account"); err != nil {
		return finish(opts, err)
	}
	app := newApp(opts.ConfigPath)
	if err := app.requireConfig(); err != nil {
		return finish(opts, err)
	}

	kind := app.firstKaeManagedIsolation()
	if kind == "" {
		return finish(opts, errf(constants.ExitUnsupported,
			"this directory is not bonded or pinned; use `kae bond` or `kae pin` first"))
	}
	if kind != modeBond && kind != modePin {
		return finish(opts, errf(constants.ExitUnsupported,
			"kae as only applies inside a bonded or pinned directory (this is %s mode)", kind))
	}

	absDir, err := cwdAbs()
	if err != nil {
		return finish(opts, err)
	}
	pinID := paths.PinID(absDir)

	be, err := app.secretBackend()
	if err != nil {
		return finish(opts, err)
	}

	switch kind {
	case modeBond:
		bondDir := app.Paths.BondDir(pinID, tool)
		if err := app.swapDirCredential(ctx, be, tool, accountName, bondDir); err != nil {
			return finish(opts, fmt.Errorf("swap bond credential for %s: %w", tool, err))
		}
		fmt.Printf("Switched %s credential to account %s (bond dir; sessions/settings unchanged)\n", tool, accountName)
	case modePin:
		newDir, err := app.preparePinConfig(ctx, tool, accountName, pinID)
		if err != nil {
			return finish(opts, fmt.Errorf("prepare pin config for %s/%s: %w", tool, accountName, err))
		}
		if err := swapPinEnvEntry(app, tool, newDir); err != nil {
			return finish(opts, fmt.Errorf("update .mise.toml for %s: %w", tool, err))
		}
		fmt.Printf("Switched %s credential to account %s (pin dir re-keyed; sessions/settings unchanged)\n", tool, accountName)
	}
	return constants.ExitOK
}

// swapDirCredential reads the captured credential for tool/accountName from
// the secret backend and writes it as the private credential file in credDir.
func (app *App) swapDirCredential(ctx context.Context, be secret.Backend, tool, accountName, credDir string) error {
	acc, found, err := account.Load(app.Paths.AccountDir(tool, accountName))
	if err != nil {
		return err
	}
	if !found {
		return errf(constants.ExitNotFound,
			"account %s/%s is not captured yet (run: kae add --no-login %s %s)",
			tool, accountName, tool, accountName)
	}
	artName := credentialArtifactName(tool)
	if artName == "" {
		return nil
	}
	metaArt, ok := acc.Artifacts[artName]
	if !ok || !metaArt.Present {
		return errf(constants.ExitAuthMissing,
			"account %s/%s has no credential snapshot; re-run kae add --no-login %s %s",
			tool, accountName, tool, accountName)
	}
	data, found, err := be.Get(ctx, metaArt.SecretRef)
	if err != nil {
		return fmt.Errorf("read snapshot credential: %w", err)
	}
	if !found {
		return errf(constants.ExitError,
			"snapshot payload missing; re-run kae add --no-login %s %s", tool, accountName)
	}
	for _, credFile := range app.pinCredItems(tool) {
		dst := filepath.Join(credDir, credFile)
		if err := patch.WriteFileAtomic(dst, data, 0o600); err != nil {
			return fmt.Errorf("write credential %s: %w", dst, err)
		}
	}
	return nil
}

// credentialArtifactName returns the snapshot artifact name that holds the
// tool's primary credential (matched by pinCredItems). Empty = no credential.
func credentialArtifactName(tool string) string {
	switch tool {
	case constants.ToolClaude:
		return "claude_ai_oauth"
	case constants.ToolCodex:
		return "auth"
	default:
		return ""
	}
}

// swapPinEnvEntry updates the isolation env entry in .mise.toml for tool to
// point at newDir. It replaces the old value by matching the current env var
// value exposed by the directory's mise config.
func swapPinEnvEntry(app *App, tool, newDir string) error {
	envVar := isolationEnvVar(tool)
	if envVar == "" {
		return nil
	}
	oldDir := app.Env.Getenv(envVar)
	if oldDir == "" {
		return errf(constants.ExitError,
			"%s is not set; is this directory actively pinned?", envVar)
	}
	data, err := os.ReadFile(".mise.toml")
	if err != nil {
		if os.IsNotExist(err) {
			return errf(constants.ExitNotFound, ".mise.toml not found")
		}
		return err
	}
	content := string(data)
	oldEntry := envVar + ` = "` + oldDir + `"`
	newEntry := envVar + ` = "` + newDir + `"`
	if !strings.Contains(content, oldEntry) {
		return errf(constants.ExitError,
			"could not find %s entry in .mise.toml (expected %q)", envVar, oldEntry)
	}
	return patch.WriteFileAtomic(".mise.toml", []byte(strings.ReplaceAll(content, oldEntry, newEntry)), 0o644)
}
