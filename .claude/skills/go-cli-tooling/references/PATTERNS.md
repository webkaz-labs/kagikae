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
   `$XDG_DATA_HOME/bash-completion/completions/<tool>`, zsh
   `$XDG_DATA_HOME/zsh/site-functions/_<tool>` (needs the dir on `fpath`), fish
   `$XDG_CONFIG_HOME/fish/completions/<tool>.fish` (auto-loaded).

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
