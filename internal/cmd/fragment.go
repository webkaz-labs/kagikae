package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/patch"
	"github.com/webkaz-labs/kagikae/internal/paths"
)

// fragmentRelPath is the kae-owned mise fragment written by `kae pin`, relative
// to the bound directory. mise loads .config/mise/conf.d/*.toml and merges it,
// so kae owns this whole file and never touches the user's mise.toml.
var fragmentRelPath = filepath.Join(".config", "mise", "conf.d", "kagikae.toml")

// kae: comment-record prefixes embedded in the fragment header and parsed back
// by `kae status` (the [env] block is for mise; these carry kae's own
// per-directory metadata, including the bound account for each tool).
const (
	fragProfilePrefix = "# kae:profile="
	fragModePrefix    = "# kae:mode="
	fragAccountPrefix = "# kae:account:" // # kae:account:<tool>=<account>
)

// userScopeMode maps an internal per-directory mechanism (modeBond/modePin) to
// the user-facing environment label (shared/isolated) used in the fragment and
// `kae status`.
func userScopeMode(mode string) string {
	if mode == modePin {
		return paths.IsolatedSegment
	}
	return paths.SharedSegment
}

// renderPinFragment renders the kae-owned mise fragment for a per-directory
// bind: machine-readable kae: records (parsed by status) followed by the [env]
// block mise exports. scope is the user-facing environment (shared/isolated).
func renderPinFragment(profileName, scope string, entries []isolationEntry) string {
	var b strings.Builder
	fmt.Fprintln(&b, "# kagikae-managed mise fragment — do not edit by hand.")
	fmt.Fprintln(&b, "# Written by `kae pin`, removed by `kae unpin`; your mise.toml is never touched.")
	fmt.Fprintf(&b, "%s%s\n", fragProfilePrefix, profileName)
	fmt.Fprintf(&b, "%s%s\n", fragModePrefix, scope)
	for _, e := range entries {
		if e.Warning == "" {
			fmt.Fprintf(&b, "%s%s=%s\n", fragAccountPrefix, e.Tool, e.Account)
		}
	}
	writeEnvEntries(&b, profileName, entries)
	return b.String()
}

// writePinFragment writes the kae-owned mise fragment in the current directory
// (creating .config/mise/conf.d/ as needed) and adds it to .gitignore. The
// fragment holds machine-specific absolute paths and account names, so it must
// never be committed.
func writePinFragment(content string) error {
	if err := os.MkdirAll(filepath.Dir(fragmentRelPath), 0o755); err != nil {
		return fmt.Errorf("create mise conf.d dir: %w", err)
	}
	if err := patch.WriteFileAtomic(fragmentRelPath, []byte(content), 0o644); err != nil {
		return err
	}
	return ensureGitignored(fragmentRelPath)
}

// removePinFragment deletes the kae-owned mise fragment in the current
// directory. ok reports whether a fragment was present (so `kae unpin` can
// distinguish a real removal from a no-op). Empty parent dirs are left in
// place: conf.d may hold other fragments and .config is shared.
func removePinFragment() (ok bool, err error) {
	if err := os.Remove(fragmentRelPath); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ensureGitignored appends fragmentRelPath to ./.gitignore unless it is already
// listed, creating .gitignore when absent. Idempotent on the entry line.
func ensureGitignored(path string) error {
	const giPath = ".gitignore"
	entry := "/" + filepath.ToSlash(path)
	data, err := os.ReadFile(giPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read .gitignore: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == entry {
			return nil // already ignored
		}
	}
	var b strings.Builder
	b.Write(data)
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		b.WriteByte('\n')
	}
	fmt.Fprintln(&b, "# kagikae per-directory mise fragment (machine-specific; do not commit)")
	fmt.Fprintln(&b, entry)
	return patch.WriteFileAtomic(giPath, []byte(b.String()), 0o644)
}

// miseActivated reports whether mise's shell activation is in effect: `mise
// activate` sets MISE_SHELL. When false, a freshly written fragment will not
// take effect until the shell re-activates, so kae prints the export fallback.
func (app *App) miseActivated() bool {
	return app.Env.Getenv("MISE_SHELL") != ""
}

// exportFallback renders the `export VAR=value` lines that reproduce the
// fragment's [env] block in the current shell, for when mise activation is not
// detected. Warning entries (tools that keep the real home) are skipped.
func exportFallback(profileName string, entries []isolationEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "export %s=%s\n", constants.EnvKaeProfile, shellSingleQuote(profileName))
	for _, e := range entries {
		if e.Warning != "" {
			continue
		}
		fmt.Fprintf(&b, "export %s=%s\n", e.EnvVar, shellSingleQuote(e.Dir))
	}
	return b.String()
}

// shellSingleQuote single-quotes s for POSIX shells (paths may contain spaces
// when HOME does), escaping embedded single quotes.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// fragmentInfo is the kae-owned metadata parsed back from a per-directory
// fragment: the bound profile, the user-facing environment, and the bound
// account for each isolated tool. It is the source of truth for `kae status`
// (the real per-tool account) and `kae pin <tool> <account>` re-binds.
type fragmentInfo struct {
	Profile  string
	Mode     string            // userScopeMode: shared|isolated
	Accounts map[string]string // tool -> bound account (isolated tools only)
}

// readPinFragment reads and parses the kae-owned fragment in the current
// directory. exists is false when no fragment is present (not an error).
func readPinFragment() (info fragmentInfo, exists bool, err error) {
	data, err := os.ReadFile(fragmentRelPath)
	if os.IsNotExist(err) {
		return fragmentInfo{Accounts: map[string]string{}}, false, nil
	}
	if err != nil {
		return fragmentInfo{}, false, err
	}
	return parsePinFragment(string(data)), true, nil
}

// parsePinFragment extracts the kae: comment records from a fragment. The [env]
// block is mise's; kae's own metadata lives in the # kae: header lines.
func parsePinFragment(content string) fragmentInfo {
	info := fragmentInfo{Accounts: map[string]string{}}
	for _, line := range strings.Split(content, "\n") {
		switch {
		case strings.HasPrefix(line, fragProfilePrefix):
			info.Profile = strings.TrimPrefix(line, fragProfilePrefix)
		case strings.HasPrefix(line, fragModePrefix):
			info.Mode = strings.TrimPrefix(line, fragModePrefix)
		case strings.HasPrefix(line, fragAccountPrefix):
			if tool, account, ok := strings.Cut(strings.TrimPrefix(line, fragAccountPrefix), "="); ok {
				info.Accounts[tool] = account
			}
		}
	}
	return info
}

// rebindFragment rewrites the fragment in place for a one-tool re-bind: the
// tool's account record, its env entry (when dir != "", i.e. isolated), and the
// recomputed profile (empty when the new account set matches no named profile).
// Every other line — other tools, warning comments, the header — is preserved.
//
// Precondition: tool is bound in the fragment (it has a # kae:account: record),
// and when dir != "" it has a non-empty isolationEnvVar. runPinRebind enforces
// both before calling, so a tool that keeps the real home never reaches here
// and cannot leave an account record without a matching env entry.
func rebindFragment(tool, account, dir, profile string) error {
	data, err := os.ReadFile(fragmentRelPath)
	if err != nil {
		return err
	}
	envVar := isolationEnvVar(tool)
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, fragAccountPrefix+tool+"="):
			lines[i] = fragAccountPrefix + tool + "=" + account
		case strings.HasPrefix(line, fragProfilePrefix):
			lines[i] = fragProfilePrefix + profile
		case strings.HasPrefix(line, constants.EnvKaeProfile+" = "):
			lines[i] = fmt.Sprintf("%s = %q", constants.EnvKaeProfile, profile)
		case dir != "" && envVar != "" && strings.HasPrefix(line, envVar+" = "):
			lines[i] = fmt.Sprintf("%s = %q", envVar, dir)
		}
	}
	return patch.WriteFileAtomic(fragmentRelPath, []byte(strings.Join(lines, "\n")), 0o644)
}
