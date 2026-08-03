package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/companion"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/patch"
	"github.com/webkaz-labs/kagikae/internal/runner"
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
// (creating .config/mise/conf.d/ as needed) and tells git to ignore it. The
// fragment holds machine-specific absolute paths and account names, so it must
// never be committed. It reports the exclude file the rule was recorded in, empty
// when none was — which is never an error, so only the fragment write can fail
// here (see ensureGitExcluded for why an ignore rule must not).
func writeDirFragment(ctx context.Context, content string) (string, error) {
	if err := writeMiseFragment(fragmentRelPath, content); err != nil {
		return "", err
	}
	return ensureGitExcluded(ctx, fragmentRelPath), nil
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

// ensureGitExcluded records an ignore rule for path (relative to the current
// directory) in the repository's shared exclude file, so the kae-owned fragment
// does not sit in `git status` as untracked. It returns the file it recorded the
// rule in, or "" when it recorded none. Idempotent on the entry line.
//
// **It never fails.** An ignore rule is cosmetic, and it is the last step of
// `kae pin`: by the time it runs the stores are materialized, the credential is
// written and the fragment is in place, so the directory *is* bound. Returning an
// error there would skip `pruneDirCredentials` — leaving the superseded
// per-directory keychain item holding a credential nothing points at, the exact
// state that sweep exists to prevent — and would swallow the export fallback a
// non-mise shell needs, on every re-run, since the cause does not go away. So a
// problem here is a warning on stderr and an empty return, which the caller
// reports by simply not claiming the fragment was ignored.
//
// This matters more than it did for `./.gitignore`, which lived *inside* the
// directory being pinned. The exclude file is outside it: pinning a linked
// worktree writes into the **main checkout's** `.git`, which can be unwritable
// while the worktree is perfectly writable (a repository cloned by another user,
// `.git` on a read-only mount, a locked-down `info/exclude`).
//
// It writes $GIT_COMMON_DIR/info/exclude rather than a tracked ./.gitignore
// because kae binds *directories*, and a git worktree is one more directory:
// pinning main plus three worktrees used to leave four dirty working trees, each
// waiting for the user to commit a line about their own machine. The exclude file
// is untracked, and one entry there covers the whole repository — the two facts
// that make this work are measured in docs/VALIDATION.md: a linked worktree's own
// $GIT_DIR/info/exclude is *not* consulted, while the common one is honoured by
// the main checkout and by every worktree.
//
// Both halves of the answer come from git, and neither is guessable:
//   - --git-common-dir is relative to the current directory in an ordinary
//     checkout (".git", "../.git") and absolute in a linked worktree, so it is
//     resolved against the cwd either way.
//   - an entry in info/exclude is anchored at the *repository root*, unlike a
//     .gitignore entry, which is anchored at its own directory. kae is run from
//     the directory being bound, which may be any depth below the root, so the
//     entry needs --show-prefix in front of it. Without that, pinning a
//     subdirectory writes a rule that matches nothing.
func ensureGitExcluded(ctx context.Context, path string) string {
	// One call for both values; the prefix keeps its trailing slash, and is
	// empty at the repository root.
	out, _, code := runner.Run(ctx, "git", "rev-parse", "--git-common-dir", "--show-prefix")
	if code != 0 {
		return "" // no repository here, or no git to ask: nothing to tell
	}
	// Trim only the line ending. A leading space is a legal first character of a
	// path component, on both lines, and TrimSpace would silently eat it.
	lines := strings.Split(out, "\n")
	// Exactly the measured shape: two values and the trailing newline's empty
	// tail. Not "at least two" — `git rev-parse` prints both values raw, with no
	// quoting and no -z, and a newline is a legal byte in a path component. A
	// repository at `…/we<LF>ird/repo` reached through a linked worktree would
	// otherwise leave lines[0] truncated to `…/we`, and kae would create
	// `…/we/info/exclude` somewhere unrelated while reporting the fragment ignored.
	if len(lines) != 3 || strings.TrimRight(lines[0], "\r") == "" {
		warnGitExclude(fmt.Errorf("git rev-parse returned %q", out))
		return ""
	}
	commonDir, err := filepath.Abs(strings.TrimRight(lines[0], "\r"))
	if err != nil {
		warnGitExclude(fmt.Errorf("resolve git common dir %q: %w", lines[0], err))
		return ""
	}
	// The answer must name a directory that already exists. git just reported this
	// as its own common dir, so it does — and requiring it keeps kae from acting on
	// an answer it could not verify: a `git` on PATH that exits 0 with a shape-valid
	// but wrong first value (a wrapper, a stub, a shell function) otherwise had kae
	// MkdirAll that path and report `ignored via`, creating a tree inside the
	// working tree that nothing reads. Measured 2026-08-04 with a stub printing
	// "junk\n\n": `<repo>/junk/info/exclude` created and `?? junk/` left in
	// `git status`. Never declare an artifact for a location you could not measure
	// (AGENTS.md); failing closed here lands in the warning path above.
	if info, serr := os.Stat(commonDir); serr != nil || !info.IsDir() {
		warnGitExclude(fmt.Errorf("git named %q as its common dir, but that is not an existing directory", commonDir))
		return ""
	}
	// ponytail: a bare repository answers this too (common dir ".", empty
	// prefix), so the rule lands in its info/exclude with no worktree to apply
	// to. Harmless — it is that repository's real exclude file — and reaching it
	// means having pinned a bare repo, so it is not guarded.
	excludeFile := filepath.Join(commonDir, "info", "exclude")
	entry := "/" + escapeGitPattern(strings.TrimRight(lines[1], "\r")) + filepath.ToSlash(path)
	data, err := os.ReadFile(excludeFile)
	if err != nil && !os.IsNotExist(err) {
		warnGitExclude(err) // *PathError already names the operation and the file
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == entry {
			return excludeFile // already ignored
		}
	}
	// Append rather than rewrite. This file is the one thing kae writes that is
	// shared by *sibling bindings* — every worktree of the repository records its
	// rule here — and the pin lock is per directory, so two `kae pin` runs in two
	// worktrees are deliberately not serialized against each other. A
	// read-modify-write would let the second rename discard the first one's entry;
	// an O_APPEND write cannot, and at worst a lost idempotency race duplicates a
	// line, which git does not mind. It also leaves the file's existing mode and
	// ownership alone, which a temp-file-and-rename would not.
	var b strings.Builder
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		b.WriteByte('\n')
	}
	fmt.Fprintln(&b, "# kagikae per-directory mise fragment (machine-specific; do not commit)")
	fmt.Fprintln(&b, entry)
	if err := os.MkdirAll(filepath.Dir(excludeFile), 0o755); err != nil {
		warnGitExclude(err)
		return ""
	}
	f, err := os.OpenFile(excludeFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		warnGitExclude(err)
		return ""
	}
	if _, err := f.WriteString(b.String()); err != nil {
		f.Close()
		warnGitExclude(err)
		return ""
	}
	if err := f.Close(); err != nil {
		warnGitExclude(err)
		return ""
	}
	return excludeFile
}

// warnGitExclude reports why kae could not record the ignore rule, on stderr and
// without changing the exit code (AGENTS.md). It names the remedy because the
// fragment is machine-specific and must not be committed: the user has to ignore
// it some other way, and kae will not silently leave that unsaid.
func warnGitExclude(err error) {
	fmt.Fprintf(os.Stderr, "kae: warning: could not tell git to ignore %s: %v\n", fragmentRelPath, err)
	fmt.Fprintf(os.Stderr, "kae: the binding is in place; ignore %s yourself (it is machine-specific and must not be committed)\n", fragmentRelPath)
}

// escapeGitPattern backslash-escapes the wildmatch metacharacters in a literal
// path component so it matches itself in a gitignore/exclude *pattern*. The
// directory kae was run in is a user path, and an unescaped one is a rule that
// matches nothing while `kae pin` reports the fragment as ignored: measured
// 2026-08-04, a repository subdirectory named `[wip]-feature` produced
// `/[wip]-feature/…`, which git read as a character class and did not ignore.
// `#` and `!` need no escaping here — the entry always starts with `/`, so they
// are never the first character of a line.
func escapeGitPattern(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`\*?[]`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
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
	Profile string
	Mode    string // userScopeMode: shared|isolated
	// Accounts maps each tool this directory binds to its account — **every** bound
	// tool, in either mode, because renderDirFragment writes the record for any
	// entry without a Warning and both mode planners build entries the same way. A
	// tool is absent only when it could not be bound at all (no isolation env var),
	// which is what pinChecks and boundStoreDir both read it as. This said
	// "isolated tools only" for several releases while every consumer relied on the
	// opposite, and it sent a reviewer looking for a bug that was not there.
	Accounts map[string]string
}

// readDirFragment reads and parses the kae-owned fragment in the current
// directory. exists is false when no fragment is present (not an error).
func readDirFragment() (info fragmentInfo, exists bool, err error) {
	return readFragmentAt("")
}

// readFragmentAt is readDirFragment for a directory kae is not standing in:
// the bound directories a breadcrumb names (pinindex.go). An empty dir means
// the current one.
func readFragmentAt(dir string) (info fragmentInfo, exists bool, err error) {
	data, err := os.ReadFile(filepath.Join(dir, fragmentRelPath))
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
