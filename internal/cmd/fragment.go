package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/companion"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/patch"
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

// renderDirFragment renders the kae-owned mise fragment for a per-directory
// bind: machine-readable kae: records (parsed by status) followed by the [env]
// block mise exports. scope is the user-facing environment (shared/isolated).
func renderDirFragment(profileName, scope string, entries []isolationEntry, companionLines, redactions []string) string {
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
	// redactions is a top-level key, so it must precede the [env] table.
	if rl := redactionsLine(redactions); rl != "" {
		fmt.Fprintln(&b, rl)
	}
	writeEnvEntries(&b, profileName, entries, companionLines)
	return b.String()
}

// redactionsLine renders the top-level mise redactions array (the env var names
// whose values mise masks in task output), or "" when there is nothing to
// redact. Shared by the full render and the re-bind's companion-section rewrite.
func redactionsLine(redactions []string) string {
	if len(redactions) == 0 {
		return ""
	}
	return fmt.Sprintf("redactions = [%s]", quoteList(redactions))
}

// quoteList renders a TOML string array body ("a", "b") for an inline array.
func quoteList(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(quoted, ", ")
}

// writeMiseFragment creates the conf.d parent dir and atomically writes a
// kae-owned mise fragment (0644). Shared by the per-directory writer
// (writeDirFragment) and the global writer (regenGlobalFragment).
func writeMiseFragment(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create mise conf.d dir: %w", err)
	}
	return patch.WriteFileAtomic(path, []byte(content), 0o644)
}

// writeDirFragment writes the kae-owned mise fragment in the current directory
// (creating .config/mise/conf.d/ as needed) and adds it to .gitignore. The
// fragment holds machine-specific absolute paths and account names, so it must
// never be committed.
func writeDirFragment(content string) error {
	if err := writeMiseFragment(fragmentRelPath, content); err != nil {
		return err
	}
	return ensureGitignored(fragmentRelPath)
}

// removeDirFragment deletes the kae-owned mise fragment in the current
// directory. ok reports whether a fragment was present (so `kae unpin` can
// distinguish a real removal from a no-op). Empty parent dirs are left in
// place: conf.d may hold other fragments and .config is shared.
func removeDirFragment() (ok bool, err error) {
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
func exportFallback(profileName string, entries []isolationEntry, companionExports []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "export %s=%s\n", constants.EnvKaeProfile, shellSingleQuote(profileName))
	for _, e := range entries {
		if e.Warning != "" {
			continue
		}
		fmt.Fprintf(&b, "export %s=%s\n", e.EnvVar, shellSingleQuote(e.Dir))
	}
	for _, line := range companionExports {
		fmt.Fprintln(&b, line)
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

// readDirFragment reads and parses the kae-owned fragment in the current
// directory. exists is false when no fragment is present (not an error).
func readDirFragment() (info fragmentInfo, exists bool, err error) {
	data, err := os.ReadFile(fragmentRelPath)
	if os.IsNotExist(err) {
		return fragmentInfo{Accounts: map[string]string{}}, false, nil
	}
	if err != nil {
		return fragmentInfo{}, false, err
	}
	return parseDirFragment(string(data)), true, nil
}

// parseDirFragment extracts the kae: comment records from a fragment. The [env]
// block is mise's; kae's own metadata lives in the # kae: header lines.
func parseDirFragment(content string) fragmentInfo {
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
// tool's account record, its env entry (when dir != "", i.e. isolated), the
// recomputed profile (empty when the new account set matches no named profile),
// and the companion section. Companions are profile-scoped, so the whole
// companion block is replaced from the new profile's plan — companionLines and
// redactions come from companionPlan(profile), and are both empty for an ad-hoc
// re-bind, which clears the now-stale bindings. Every other line — other tools'
// isolation entries, warning comments, the header — is preserved.
//
// Precondition: tool is bound in the fragment (it has a # kae:account: record),
// and when dir != "" it has a non-empty isolationEnvVar. runRebind enforces
// both before calling, so a tool that keeps the real home never reaches here
// and cannot leave an account record without a matching env entry.
func rebindFragment(tool, account, dir, profile string, companionLines, redactions []string) error {
	data, err := os.ReadFile(fragmentRelPath)
	if err != nil {
		return err
	}
	envVar := isolationEnvVar(tool)
	companionVars := make(map[string]bool)
	for _, v := range companion.EnvVars() {
		companionVars[v] = true
	}
	src := strings.Split(string(data), "\n")
	out := make([]string, 0, len(src)+len(companionLines)+1)
	for _, line := range src {
		switch {
		case strings.HasPrefix(line, fragAccountPrefix+tool+"="):
			out = append(out, fragAccountPrefix+tool+"="+account)
		case strings.HasPrefix(line, fragProfilePrefix):
			out = append(out, fragProfilePrefix+profile)
		case strings.HasPrefix(line, constants.EnvKaeProfile+" = "):
			out = append(out, fmt.Sprintf("%s = %q", constants.EnvKaeProfile, profile))
		case dir != "" && envVar != "" && strings.HasPrefix(line, envVar+" = "):
			out = append(out, fmt.Sprintf("%s = %q", envVar, dir))
		case strings.HasPrefix(line, "redactions = ["):
			// Drop: re-inserted before [env] from the new profile's plan.
		case isCompanionEnvLine(line, companionVars):
			// Drop: stale companion binding, re-appended at the [env] block end.
		default:
			out = append(out, line)
		}
	}
	rebuilt, err := applyCompanionSection(out, companionLines, redactions)
	if err != nil {
		return err
	}
	return patch.WriteFileAtomic(fragmentRelPath, []byte(strings.Join(rebuilt, "\n")), 0o644)
}

// isCompanionEnvLine reports whether a fragment [env] line sets one of the
// companion-owned env vars. KAE_PROFILE and per-tool isolation lines are handled
// by earlier rebindFragment switch cases, so only true companion lines reach
// here.
func isCompanionEnvLine(line string, companionVars map[string]bool) bool {
	key, _, ok := strings.Cut(line, " = ")
	return ok && companionVars[key]
}

// applyCompanionSection re-inserts the companion block into a fragment whose old
// companion lines and redactions line were already stripped: the redactions
// array goes top-level just before [env], and the companion env lines go at the
// end of the [env] block — which is end-of-file, since [env] is the last table
// renderDirFragment writes. The caller has already stripped the old companion
// lines and preserves the [env] block, so with nothing to place (an ad-hoc
// re-bind) this just restores the trailing newline. A companion section with no
// [env] block to anchor it is a corrupt fragment: failing loud beats silently
// floating a token line outside [env], where mise would never export it.
func applyCompanionSection(lines, companionLines, redactions []string) ([]string, error) {
	// strings.Split leaves a trailing "" for the file's final newline; drop it
	// so appends land at true end-of-content, then the join restores it.
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	if len(companionLines) == 0 && len(redactions) == 0 {
		return append(lines, ""), nil
	}
	envIdx := -1
	for i, line := range lines {
		if line == "[env]" {
			envIdx = i
			break
		}
	}
	if envIdx < 0 {
		return nil, fmt.Errorf("%s has no [env] block; cannot place companion bindings", fragmentRelPath)
	}
	// Rebuild once: redactions just before [env], the companion lines at the
	// [env] block's end (end-of-file), then the trailing newline restored.
	rebuilt := make([]string, 0, len(lines)+len(companionLines)+2)
	rebuilt = append(rebuilt, lines[:envIdx]...)
	if rl := redactionsLine(redactions); rl != "" {
		rebuilt = append(rebuilt, rl)
	}
	rebuilt = append(rebuilt, lines[envIdx:]...)
	rebuilt = append(rebuilt, companionLines...)
	return append(rebuilt, ""), nil
}
