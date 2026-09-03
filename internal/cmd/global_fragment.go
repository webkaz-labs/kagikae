package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/state"
)

// syncedEntry is one resolved (envVar, home-dir) pair from the synced map,
// iterated in constants.Tools canonical order.
type syncedEntry struct {
	envVar string
	dir    string
}

// credentialEntry is the second env pair a globally isolated tool needs: its
// credential store, which is keyed by account rather than by home so every
// binding of that account reads one copy. Empty envVar means the tool has none.
func (app *App) credentialEntry(tool, account string) (syncedEntry, bool) {
	envVar := credentialEnvVar(tool)
	credDir := app.credStoreDir(tool, account)
	if envVar == "" || credDir == "" {
		return syncedEntry{}, false
	}
	return syncedEntry{envVar, credDir}, true
}

// iterSynced resolves the synced map into (envVar, dir) pairs in canonical
// tool order, skipping tools that have no entry in synced and tools with no
// stable home-isolation env var (e.g. agy/cursor).
func (app *App) iterSynced(synced map[string]string) []syncedEntry {
	var out []syncedEntry
	for _, tool := range constants.Tools {
		account, ok := synced[tool]
		if !ok {
			continue
		}
		if envVar := isolationEnvVar(tool); envVar != "" {
			out = append(out, syncedEntry{envVar, app.Paths.GlobalIsolatedHomeDir(tool, account)})
			if entry, ok := app.credentialEntry(tool, account); ok {
				out = append(out, entry)
			}
		}
	}
	return out
}

// renderGlobalFragment renders the kae-owned global mise fragment for global
// isolated mode (kae use -i): an [env] block pointing each globally isolated
// tool's home-isolation env var at its private home, plus — for a tool that can
// address its credential separately — the credential variable at the *account's*
// store, which is not private to the home (credentialEntry). Unlike the per-directory
// fragment it carries no KAE_PROFILE and no kae: metadata records — state.json
// `synced` is the source of truth, so the fragment is purely derived. Tools are
// emitted in canonical order for stable, diffable output.
func (app *App) renderGlobalFragment(synced map[string]string) string {
	var b strings.Builder
	fmt.Fprintln(&b, "# kagikae-managed mise fragment — do not edit by hand.")
	fmt.Fprintln(&b, "# Written by `kae use -i`, removed by `kae use -s`; regenerated from kae state.")
	fmt.Fprintln(&b, "# Your mise.toml is never touched.")
	fmt.Fprintln(&b, "[env]")
	for _, e := range app.iterSynced(synced) {
		fmt.Fprintf(&b, "%s = %q\n", e.envVar, e.dir)
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
	return writeMiseFragment(path, app.renderGlobalFragment(synced))
}

// globalFragmentConsistent compares the derived fragment byte-for-byte with
// the current state. Atomic fragment writes make raw equality sufficient. An
// empty synced map is consistent only when the fragment is absent.
func (app *App) globalFragmentConsistent(synced map[string]string) (bool, error) {
	path := app.Paths.MiseGlobalFragmentFile()
	if len(synced) == 0 {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return true, nil
		} else if err != nil {
			return false, err
		}
		return false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	return string(data) == app.renderGlobalFragment(synced), nil
}

// teardownSynced drops the given tools from state.synced and regenerates (or
// deletes) the global mise fragment — the documented teardown of kae use -i,
// run by kae use -s / bare kae use after it switches the real home in place. A
// no-op (no state write) when none of the tools is globally isolated and the
// derived fragment already agrees with state. A stale/crash-left fragment is
// regenerated from current state under the same lock even on that fast path.
func (app *App) teardownSynced(tools []string) error {
	_, err := app.mutateSyncedAndFragment(nil, func(st *state.State) bool {
		changed := false
		for _, tool := range tools {
			if _, ok := st.Synced[tool]; ok {
				delete(st.Synced, tool)
				changed = true
			}
		}
		return changed
	})
	if err != nil {
		return err
	}
	return nil
}

// globalExportFallback renders the `export VAR=value` lines reproducing the
// global fragment's [env] block in the current shell, for when mise activation
// is not detected (so the binding takes effect immediately).
func (app *App) globalExportFallback(synced map[string]string) string {
	var b strings.Builder
	for _, e := range app.iterSynced(synced) {
		fmt.Fprintf(&b, "export %s=%s\n", e.envVar, shellSingleQuote(e.dir))
	}
	return b.String()
}
