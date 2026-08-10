# kagikae vocabulary

This file is the authority on **naming** for the vocabulary below: the words this
repository uses for the things it switches, and the words that look like synonyms
and are not. It is a glossary and nothing else — it decides no behaviour, and an
entry that starts to read like a rule belongs in the document that owns the rule.

It is not the only place a name is settled, and saying it was cost this file a
correction: the **JSON contract tokens** are an enum whose home is
`internal/constants`, so they are named there and documented in
[DATA-MODEL.md](DATA-MODEL.md) § Status Vocabulary, and nothing about a status, an
error code, an artifact kind or a driver id belongs in the tables below. The
numeric **exit** codes are a third thing again, and theirs is
[CLI.md](CLI.md) § Exit Codes.

That constraint is not stylistic. Where a glossary carries a rule, a mismatch
between it and the code stops being a bug in the code and becomes a document
with the standing to license the bug — the shape `docs/CLI.md` recorded an
instance of when its § `kae rollback --json` paragraph quoted a string the
binary did not contain, while two files that stated the rule correctly cited
that paragraph as their authority.
So where a term names something a predicate decides, the entry says which
predicate, and stops.

Where the answers live:

| question | authority |
|----------|-----------|
| what a decision does | the predicate named in the entry |
| where something lives on disk | [DATA-MODEL.md](DATA-MODEL.md) § Directory Layout (XDG) |
| what a mode switches | [PRODUCT.md](PRODUCT.md) § Switching Surface |
| what one tool switches and preserves | [ADAPTERS.md](ADAPTERS.md) |
| what may happen to a credential copy | [CREDENTIAL-RULES.md](CREDENTIAL-RULES.md) |
| what a JSON status, code, artifact kind or driver id is called | `internal/constants`, described in [DATA-MODEL.md](DATA-MODEL.md) § Status Vocabulary |

## Surface terms

The words a user types or reads. They are the same five this file inherited from
`PRODUCT.md § Terminology`, which now points here so that there is one place to
change a name.

| term | names |
|------|-------|
| `account` | a tool-specific login snapshot, e.g. `claude/main`, `codex/side` |
| `profile` | a named bundle mapping each tool to one account, e.g. `main` = claude:main + codex:main + agy:main |
| `driver` | the platform/tool-specific mechanism that captures and applies auth artifacts |
| `artifact` | one captured unit of authentication state (a JSON pointer value, a file, or a keychain item) |
| `companion` | a non-AI tool (git, gh, a cloud CLI) whose auth kae binds to a profile by driving env/config — not captured like an account; see [ADAPTERS-COMPANION.md](ADAPTERS-COMPANION.md) |

## Mechanism terms

| term | names | naming note |
|------|-------|-------------|
| **bound directory** | a directory `kae pin` has bound, so that working in it selects the accounts the binding names | Preferred over **pinned directory**, which this repository also uses and has *not* converged (see § Not converged) |
| **binding** | what a bound directory records: which tool gets which account, in which mode | |
| **pin-id** | the name kae files a bound directory's state under — `paths.PinID`, a hex prefix of the sha256 of the absolute path. Not user-facing, and not stable across a move | |
| **breadcrumb** | the file recording which absolute path a pin-id hashes from; [DATA-MODEL.md](DATA-MODEL.md) § Directory Layout (XDG) calls it the bound-directory record | Not the **fragment** below, which is the other file a bound directory has; what each of the two settles, and what removes which, is [CREDENTIAL-RULES.md](CREDENTIAL-RULES.md) § A store tree is history; a fragment is the binding |
| **fragment** | the kae-owned mise config file inside a bound directory | What it settles, rather than what this file may state: [CREDENTIAL-RULES.md](CREDENTIAL-RULES.md) § A store tree is history; a fragment is the binding, and `readFragmentAt` is where a caller asks |
| **mode** (`shared`, `isolated`) | which of the two per-directory mechanisms a binding uses | These two words are the whole vocabulary. Four earlier *mode* names (`home`, `overlay`, `bond`, `pin`) are retired and must not come back as mode names — `bond dir` below is a survivor of one of them and is not a mode |
| **bond dir** | the directory a shared-mode binding materializes: symlinks to the real tool home's entries, minus a denylist, plus a private credential. `prepareBond` builds it | The same directory the attribution code calls the **shared config dir** — one thing, two aspects, not a good/bad pair. `bond` survives from the retired mode name; the path segment is `shared` |
| **credential store** | the per-account directory a bind points a tool's credential variable at, so directories sharing an account share one credential | |
| **reader** | a config dir currently reading a credential store — `credStoreReaders` enumerates them | Never **witness**. The two were used interchangeably, with the same qualifier on each ("every witness that can speak" beside "a reader that cannot speak"), so no distinction was lost by converging them |
| **snapshot** | kae's own stored copy of one account's artifacts | |
| **identity cache** | the tool's own record of which account is logged in (claude's `/oauthAccount`) | Evidence *about* an account, never a credential — and the two are compared by different predicates, which [ADAPTERS.md](ADAPTERS.md) and `AGENTS.md` own |
| **harvest** | copying an existing credential somewhere it survives, before a write or a delete would lose it | |
| **capture back** | **narrower than harvest**: the single harvest `kae relogin` runs after the tool's own login flow ([CLI.md](CLI.md) § kae relogin Semantics) | Not a synonym for harvest, and the only place the phrase is correct |
| **tombstone** | a credential the tool itself overwrote to record that its own login is dead | What kae may conclude from one is [CREDENTIAL-RULES.md](CREDENTIAL-RULES.md) § What kae observed is not what the tool can do |
| **supersedes**, **orderable** | the two predicates deciding whether one copy of a credential is newer than another (`internal/cmd/freshness.go`) | `orderable` is `supersedes`'s precondition. The normative text is [CREDENTIAL-RULES.md](CREDENTIAL-RULES.md) § Ordering two copies of one credential; do not restate either predicate as a word-list here, because a *subset* of one of them is this repository's most-repeated defect |

## Not converged

A preference above is a rule for new prose, not a claim about the tree — and one
of them is measurably not met today: **pinned directory** is still used alongside
**bound directory**. Why it is filed rather than done is
[ROADMAP.md](ROADMAP.md)'s to state, and it does; this section exists so a reader
of the table above is not told a name has settled when it has not.

Count both words in one derivation rather than trusting a number quoted here:

```bash
git ls-files | grep -v '^\.claude/skills/go-cli-tooling/' \
  | xargs grep -oihE '(bound|pinned) director[a-z]*' | sort | uniq -c
```
