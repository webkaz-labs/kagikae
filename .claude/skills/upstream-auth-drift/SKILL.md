---
name: upstream-auth-drift
description: Use this skill when an upstream AI-coding CLI (claude, codex, cursor, copilot, opencode, agy) may have changed how its authentication works, or when kae's model of where a credential lives may be wrong. Trigger on a `kae doctor` upstream_version or identity_drift warning, on upgrading one of those tools, on a symptom like "kae says it switched but the tool shows the old account" or "the tool asks me to log in again after a switch", and on a routine re-verification of the assumptions in docs/VALIDATION.md.
argument-hint: <tool> [symptom|check-code|"upgraded"|"routine"]
---

# Responding to an upstream auth change

**One sentence: when an upstream tool changes how its authentication works, this
skill takes you from "something is off" to "kae is correct again and the
assumption is re-recorded", as one workflow.**

Detection is one of four entry points and the *least* valuable one. Both real
failures kae has had were noticed by a human before any check existed. The
expensive, error-prone part is the middle — measuring the new behaviour without a
login, and getting kae's model to match it exactly — and that is where a session
without prior context flails.

## Entry points (all four are normal)

1. **A signal fired** — `upstream_version` warns (the tool moved past the
   verified release, or the assumptions are six months old), `identity_drift`
   warns, or a fingerprint moved.
2. **A tool upgraded** — the highest-yield moment, because the old bundle is
   often still on disk to diff against.
3. **A human suspects something** — "kae says I switched but the tool shows the
   old account". *This is how both real failures were found.* Accept a symptom as
   input, not only a check code.
4. **Routine** — nothing has re-verified tool X in a while.

## Phase 1 — Turn the symptom into one falsifiable statement

Read [docs/VALIDATION.md](../../../docs/VALIDATION.md) § "Upstream Behaviour
Assumptions" for the tool and pick the row(s) that would produce this symptom.
**If no row would, that is itself the finding** — a load-bearing assumption
nobody wrote down. State what you expect to see if the assumption still holds,
*before* measuring.

## Phase 2a — Read the official source first

Reverse-engineering is the fallback, not the first move. One file read from the
public codex repo settled in a minute what binary inspection had left ambiguous
for a session — including a detail measurement would have missed.

| Tool | Public source | Notes |
|---|---|---|
| codex | `openai/codex` (Rust). Tags are `rust-v<version>`, matching `codex --version` | authoritative; read at the **installed** tag |
| opencode | public (confirm the canonical owner before citing it — the `sst/opencode` path redirects) | authoritative |
| claude | not open source; public docs and release notes only. Auth internals are **undocumented** | docs give intent, the bundle gives what shipped |
| cursor / copilot / agy | closed | bundle inspection only |

Two disciplines:

- **Pin the version.** Read the source at the tag matching the installed binary.
  `main` describes a future the user is not running.
- **When docs and the shipped artifact disagree, the artifact wins.** kae depends
  on behaviour, not intent. When both agree, say so in the VALIDATION row — an
  assumption confirmed from two independent sources is the one you can leave
  alone longest.

## Phase 2b — Measure what no source documents

For claude's TTLs and keychain naming, and for every closed tool, there is
nothing to read. The toolkit and where each technique applies is in
[references/measuring.md](references/measuring.md). Start there rather than
inventing a procedure: every entry is a technique that already replaced a slower
one, and several record a trap that cost a session.

**Record the condition, never an absolute.** The v0.12.0 post-mortem is exactly
this: "`/oauthAccount` self-heals" was recorded as a fact when the fact was "it
self-heals past a 24h TTL that every refresh renews". An absolute is what expires
silently.

## Phase 3 — Update kae to match

1. **Find every place kae encodes the old rule.** The v0.12.0 bug lived in one
   constant, but the fix touched four call sites because the same credential copy
   was written four times. Grep for the constant, then for the callers of
   whatever resolves it.
2. **Put the rule in the adapter, never at the call site.** This is a boundary in
   [docs/CREDENTIAL-RULES.md](../../../docs/CREDENTIAL-RULES.md) § Resolving a
   credential's location: only the adapter may evaluate where a credential lives. If a caller needs it for a different environment, build an
   `adapter.Env` for that environment and ask again (`dirCredentialSpec` is the
   worked example) — do not recompute a name.
3. **Decide fail-loud vs warn vs refuse, and prefer refusing to guessing.** The
   rule kae now applies, from 1.5:
   - the environment shows the whole rule → **follow it**;
   - part of the rule is outside the environment but leaves a **proxy** there →
     **warn** and keep kae's own answer;
   - it leaves **no proxy** → record it in docs/ADAPTERS.md and move on. Do not
     turn an unobservable branch into a warning on an observable one.
   - **Never declare an artifact for a location you could not measure.** A
     guessed path is a write nothing reads.
4. **Check the blast radius on the other tools before generalizing.** The most
   dangerous moment in the v0.12.0 work was generalizing "write the per-directory
   keychain item" to every keychain artifact, which would have destroyed codex's
   *global* login. A capability like that is **declared by the adapter with the
   safe default**, plus a parity test that derives the truth and fails if the
   declaration disagrees.
5. **Leave a regression test that fails on the old behaviour.** Compute expected
   values *outside* kae (an external `shasum`/`hashlib`), or the test agrees with
   whatever formula the code has, including a wrong one. Prove the teeth by
   running it against the pre-fix build or by mutating the new code — and mutate
   by *inverting a predicate*, not by `if false &&`, which drops an import and
   fails to build instead of failing the assertion.

## Phase 4 — Re-record, in the same commit

`VerifiedVersion()`, `VerifiedOn()`, the `docs/ADAPTERS.md` table, and the
VALIDATION row(s). The condition, not the absolute.
`TestVerifiedVersionsMatchTheDocs` fails if the method and the table disagree, so
a half-done lockstep does not compile past the gate — but nothing checks the
VALIDATION prose, which is where the *procedure* lives. Update it too if you
found a cheaper one: the `security` PATH shim replaced a "seed the file, wait for
a token refresh" recipe that needed a real account, and that is a permanent
improvement for everyone after you.

**The `Measured on` column of
[docs/VALIDATION.md](../../../docs/VALIDATION.md) § Upstream Literal Fingerprints
is a version, and it is outside that set.** It records the build the *counts* were
taken on, not the release a row was verified against, and
`TestUpstreamLiteralFingerprints` turns it into a path that must exist rather than
skipping. cursor's cell there carries a date version while
`cursor.VerifiedVersion()` is `""`. Move it when you re-measure the counts; never
to match a version you just bumped.

## Phase 5 — Verify

`mise run check` is the only gate. Add the login-free smoke for the rule you just
changed and prove it has teeth. A smoke that cannot fail is documentation, not
verification. What genuinely needs a real account — does the credential alone
authenticate, does the payload round-trip and pass the keychain ACL — stays in
release acceptance; this skill's job is to shrink that list, not to pretend to
replace it.

## Safety rules that are not negotiable

- Never run against the real `$HOME` or write to the real keychain. Temp HOME and
  XDG roots only.
- Keychain reads are **attributes only** (`security find-generic-password -s …`).
  Never `-w`. Never `security delete-generic-password` — clean up with the tool's
  own `logout`, which deletes the item it owns and only that one.
- No credential value in a commit message, a doc, a test fixture or a report.
- Example names follow [AGENTS.md](../../../AGENTS.md) § "Example Names in Docs
  and Tests" — that table is the list. The half worth repeating because it is
  easy to get wrong: **never a real login handle**, not even a well-known example
  account, which is still somebody's real account.
