# Optional CLI Patterns

Opt-in patterns proven in the `kae` CLI (a per-project account switcher) and
promoted here so sibling Go CLIs can inherit them. Neither pattern is required —
adopt one only when the tool needs the capability. Both stay dependency-free and
reuse the standard's existing seams (`internal/cmd`, `internal/runner`,
`internal/config`).

## Mise Integration

For a tool that uses [mise](https://mise.jdx.dev) as its per-project integration
base, in two roles: redirecting tool environment per directory, and driving
shell completion. Use this only when the tool already expects mise to be active.

### Env redirect via a tool-owned mise fragment

Redirect a managed tool's home/config env (e.g. `CLAUDE_CONFIG_DIR`) through a
**generated, tool-owned** mise fragment, never by editing the user's files:

- Global scope: `~/.config/mise/conf.d/<tool>.toml`; project scope:
  `./.config/mise/conf.d/<tool>.toml` (a project fragment overrides the global
  one, so a per-directory binding wins over a global one).
- **Never read or write the user's `mise.toml`, and never mutate the real
  managed home.** Regenerate the fragment from the tool's own state; teardown
  just deletes it.
- A project fragment carries machine-specific absolute paths, so **add it to
  `.gitignore`** and give it a self-documenting header comment.
- Requires mise activation for the scope. When activation cannot be confirmed,
  warn and print the equivalent `export …` line instead of failing silently.

### Dynamic completion via a hidden `__complete` backend

Expose one hidden `<tool> __complete <kind> [args]` subcommand (omitted from
`help`) that prints candidates from the tool's live state, **one per line**.
It is the single source every completion surface consults, so candidate lists
never drift from the real router/config/state. Read-only, no locks.

- The generated shell completion script (`<tool> completion <bash|zsh|fish>`)
  calls `<tool> __complete` **at completion time** rather than baking a static
  word list at generation time, so candidates track live state.
- mise task-argument completion reuses the same backend through a task
  `usage` spec plus a `complete "<arg>" run="<tool> __complete …"` directive, so
  `mise run <task> <TAB>` resolves the same live candidates.
- The line-oriented output is an internal contract for the generated scripts and
  mise directives — it is **not** the JSON contract (`schema_version` is
  unaffected).
- **Route by the flag-filtered positional, not the absolute word index.** A flag
  before the positionals (`<tool> add --no-login <TAB>`, `<tool> use -i x <TAB>`)
  shifts `COMP_CWORD` / `CURRENT` / token count, so routing on the raw index
  completes the wrong position (or nothing). Build the list of non-flag args
  after the command (drop tokens starting with `-`) and route on *that* slot, so
  flags never move the positional.
- **Complete flag names too, from the parser's own flags.** When the current
  word starts with `-`, offer the command's flags via a `flags <command>` kind
  that enumerates a `flag.FlagSet` built by the *same* per-command registrars the
  parser uses (a small `flagspec` shared by `parseCommon` and the enumerator), so
  the completed flags never drift from what the command accepts. Normalize router
  aliases to the canonical command before the lookup.

### Registration scope (the rule that prevents flicker)

- The tool's **own** shell completion is binary-scoped, so register it
  **globally** (the shell's standard completions dir, or an opt-in global mise
  `[hooks.enter]`). Never register it per-project — a per-directory registration
  makes `<tool> <TAB>` blink in and out by directory.
- mise **task-argument** completion is project-scoped, so it lives in the
  project mise block where the tasks are.
- `<tool> completion --install` writes to the completions dir by default and
  **never** mutates the global mise config unless the user explicitly opts into
  the mise-hook path; an idempotent marker block guards re-runs, and a foreign
  pre-existing `[hooks.enter]` is refused rather than overwritten.
- **For zsh, install into a dir already on `fpath`.** A fixed
  `$XDG_DATA_HOME/zsh/site-functions` is often not on the user's `fpath`, so the
  file never loads. Prefer the first **existing** common user completions dir
  (`$XDG_CONFIG_HOME/zsh/completions`, `~/.zsh/completions`, `~/.zfunc`) — its
  existence is a robust proxy for "on fpath" — and fall back to the XDG dir with
  an fpath-add note only when none exists. Do not shell out to zsh to read
  `$fpath` (an interactive zsh's stdout is easily polluted by rc files).
- Always document a mise-free fallback (`eval "$(<tool> completion zsh)"` or a
  completions-dir file). mise hooks are experimental, so they are not the
  primary route.

### Proven traps

1. **`complete run` cannot see the prior argument** (mise/usage gives no such
   guarantee), so a mise task cannot scope candidates by an earlier positional
   (e.g. accounts-for-this-tool). Keep that scoping in the tool's **native**
   completion script, where the preceding word is available.
2. **Native completion scripts duplicate across shells with different word
   indexing**: bash uses 0-based `COMP_WORDS[N]`/`COMP_CWORD`, zsh 1-based
   `words[N]`/`CURRENT`, fish the `(commandline -opc)` token count. The same
   position→kind routing in three languages invites off-by-one bugs (an index
   error yields empty candidates with no error). Guard each shell's argument
   index with a regression test.
3. **Inline only the tiny, rarely-changing sub-verb sets** (e.g. `rm rename`) in
   the scripts; everything live goes through `__complete`. A `subcommands` kind
   is not worth the extra process spawn.
4. **Resolve XDG paths with a spec-compliant helper** (ignore relative values,
   fall back to defaults). Completion file locations: bash
   `$XDG_DATA_HOME/bash-completion/completions/<tool>`, zsh a dir on `fpath`
   (see the install bullet above; the XDG `zsh/site-functions` fallback is often
   *not* on fpath), fish `$XDG_CONFIG_HOME/fish/completions/<tool>.fish`
   (auto-loaded).
5. **zsh caches its completion index in a *compdump*** (`$ZSH_COMPDUMP` /
   `~/.zcompdump`), and `compinit -C` (common in speed-tuned setups) skips the
   rescan — so a newly installed `_<tool>` does not load until the dump is
   rebuilt. This reads as "installed but no completion." The zsh `--install`
   activation note must say so: open a new shell, and if it still does not
   appear, `rm -f "${ZSH_COMPDUMP:-$HOME/.zcompdump}" && autoload -Uz compinit &&
   compinit`. Do not auto-`rm` the user's compdump from the install subprocess
   (it cannot affect the running shell, and it mutates state the tool does not
   own) — warn and hand over the command.
6. **Rebuilding the binary does not refresh an already-registered completion
   script.** A dev/install loop that only `go build`s (e.g. a `mise run install`
   task that copies the binary to `~/.local/bin`) leaves the previously-installed
   `_<tool>` / completion file stale, so a **structural** change (a new
   subcommand `case`, a new `__complete` kind) does not take effect — the binary
   resolves the new `__complete` kinds, but the old script never calls them. Live
   candidate changes need nothing (resolved at completion time). A docs reminder
   is not enough — the user does not see it and will not act. Make it automatic:
   give the tool a `completion --refresh` mode that rewrites *every
   already-registered* completion file from the current binary (and **never
   creates** a new registration), then call it from the build/install task and
   the `curl | sh` installer so an upgrade or local rebuild propagates a
   structural change with no manual step. Two registration paths matter here: a
   **mise `[hooks.enter]`** that sources `<tool> completion <shell>` self-updates
   on directory entry (nothing to refresh); a static **fpath/completions file** is
   the one `--refresh` rewrites. For zsh under `compinit -C`, the rewritten file
   still needs a compdump rebuild — have `--refresh` print that command (per trap
   5) rather than auto-`rm` the user's cache.
7. **A new subcommand group must not ship as a completion dead end.** When a
   command dispatches sub-verbs (`<tool> account rm|rename|…`), its native script
   needs a dedicated `case` (sub-verbs at the np==0 slot, then the argument
   positions). Adding the command word to the command list is not enough — the
   sub-verbs and their argument completion live in a separate per-shell `case`,
   which is easy to forget (a tool shipped a whole subcommand group with no
   completion case this way). Guard it: keep the sub-verb sets in one
   test-visible table and assert every group's verbs appear in *all three*
   generated scripts, so a new group cannot merge without its completion case.

## Did-You-Mean Hints

When an unknown command, subcommand, tool, or profile name is close to a real
one, append a single nearest-match suggestion to the existing usage error so a
typo names its fix instead of only listing the full vocabulary.

- **Same candidate source as completion.** Build candidates from the same
  underlying slice or function the `__complete` backend uses for each kind (the
  tool's enum, config profile names), so a suggestion never drifts from the real
  surface. For commands, additionally include the short aliases that are routed
  but intentionally omitted from `__complete commands` (kept tidy for shell
  completion), so a near miss of an alias is still caught.
- **Hand-rolled, no dependency.** Compute edit distance with a small Levenshtein
  helper; do not add a fuzzy-matching library (see
  [LIBRARIES.md](LIBRARIES.md)).
- **Noise threshold.** Suggest only when the best distance is both `<= 2` and
  `<= len(input)/3 + 1` (a short typo of a long word still hints; a wildly
  different token does not). A tie for the best distance, an exact match, or no
  candidate under the threshold appends nothing.
- **Suggestion-only.** The command still fails with its original exit code and
  the JSON contract is unchanged; only the human-facing message on stderr gains
  a `— did you mean "<candidate>"?` clause. Account names and free-form values
  are out of scope, and only one best match is named (no multi-candidate list).
- **One error per kind.** Route every unknown-X error through one shared
  validator so the hint and any successor/removed-name message live in a single
  place rather than diverging across call sites.

Cover each site with a test: a near miss yields the hint, an unrelated token
leaves the error unchanged, and a valid prefix/alias still resolves with no hint.
