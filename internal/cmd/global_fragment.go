package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/patch"
	"github.com/webkaz-labs/kagikae/internal/state"
)

// renderGlobalFragment renders the kae-owned global mise fragment for global
// isolated mode (kae use -i): an [env] block pointing each globally isolated
// tool's home-isolation env var at its private home. Unlike the per-directory
// fragment it carries no KAE_PROFILE and no kae: metadata records — state.json
// `synced` is the source of truth, so the fragment is purely derived. Tools are
// emitted in canonical order for stable, diffable output.
func (app *App) renderGlobalFragment(synced map[string]string) string {
	var b strings.Builder
	fmt.Fprintln(&b, "# kagikae-managed mise fragment — do not edit by hand.")
	fmt.Fprintln(&b, "# Written by `kae use -i`, removed by `kae use -s`; regenerated from kae state.")
	fmt.Fprintln(&b, "# Your mise.toml is never touched.")
	fmt.Fprintln(&b, "[env]")
	for _, tool := range constants.Tools {
		account, ok := synced[tool]
		if !ok {
			continue
		}
		if envVar := isolationEnvVar(tool); envVar != "" {
			fmt.Fprintf(&b, "%s = %q\n", envVar, app.Paths.GlobalIsolatedHomeDir(tool, account))
		}
	}
	return b.String()
}

// regenGlobalFragment rewrites the kae-owned global mise fragment from the
// current `synced` map, creating ~/.config/mise/conf.d/ as needed. When no
// tool is globally isolated it deletes the fragment instead (a missing file is
// not an error), so an empty `synced` leaves no stale [env] block behind.
func (app *App) regenGlobalFragment(synced map[string]string) error {
	path := app.Paths.MiseGlobalFragmentFile()
	if len(synced) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove global mise fragment: %w", err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create mise conf.d dir: %w", err)
	}
	return patch.WriteFileAtomic(path, []byte(app.renderGlobalFragment(synced)), 0o644)
}

// teardownSynced drops the given tools from state.synced and regenerates (or
// deletes) the global mise fragment — the documented teardown of kae use -i,
// run by kae use -s / bare kae use after it switches the real home in place. A
// no-op (no state write) when none of the tools is globally isolated.
func (app *App) teardownSynced(tools []string) error {
	st, err := app.loadState()
	if err != nil {
		return err
	}
	changed := false
	for _, tool := range tools {
		if _, ok := st.Synced[tool]; ok {
			delete(st.Synced, tool)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	st.UpdatedAt = app.Now().UTC()
	if err := state.Save(app.Paths.StateFile(), st); err != nil {
		return err
	}
	return app.regenGlobalFragment(st.Synced)
}

// globalExportFallback renders the `export VAR=value` lines reproducing the
// global fragment's [env] block in the current shell, for when mise activation
// is not detected (so the binding takes effect immediately).
func (app *App) globalExportFallback(synced map[string]string) string {
	var b strings.Builder
	for _, tool := range constants.Tools {
		account, ok := synced[tool]
		if !ok {
			continue
		}
		if envVar := isolationEnvVar(tool); envVar != "" {
			fmt.Fprintf(&b, "export %s=%s\n", envVar, shellSingleQuote(app.Paths.GlobalIsolatedHomeDir(tool, account)))
		}
	}
	return b.String()
}
