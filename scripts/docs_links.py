#!/usr/bin/env python3
"""Emit every markdown link in the tree as `<file>\t<target>`, for check-docs.sh.

Why this is Python and not another line in that shell script. The extraction carries
state across lines — a `fenced` flag, and a code-span strip that has to match a run of
backticks with the same run — which is where the shell attempts broke. Two traps, both
measured while writing them:

  * `grep` exits 1 on a markdown file with no links, which under `set -e` with
    `pipefail` kills the producer and leaves the walk reporting zero links found;
  * an apostrophe inside a comment (`AGENTS.md's`) sitting inside a process
    substitution opens a single-quoted string and mangles everything after it.
    `bash -n` and `shellcheck` both pass on that; it only fails at run time.

An earlier version of this paragraph cited an AGENTS.md rule about using Python for
countable regex extraction. There is no such rule in AGENTS.md — `grep -n python3
AGENTS.md` returns nothing — and the BSD `grep -oE` exit-status claim it rested on did
not reproduce. The two traps above did, and they are the whole reason.

A link-shaped string inside code is an example, not a link, so fenced blocks and
inline code spans are removed first. AGENTS.md's citation rule contains a bracketed
`X.md` pair as an illustration of a grep form, and the first real run of the check
reported it as a broken target.

CODE_SPAN matches a run of backticks and requires the same run to close it, because a
single-backtick pattern left the double-backtick idiom exposed: on ``[x](y)`` it ate the
two opening backticks as an empty span and the two closing ones, handing the link
straight to LINK. AGENTS.md uses that idiom seven times, and it is what you reach for
when a span must itself contain a backtick — which is the shape its own citation rule
discusses. Since this runs in `mise run check`, the symptom was a gate that blocks a
commit on correct prose. One example does not pin a character class.

Known gaps, each measured as having no instance in this repository today. A tilde fence
is tracked as well as a backtick one, but a four-space indented code block is not, so a
link inside one is reported as broken. Reference-style links (`[x][1]` with a separate
`[1]: path` definition), links split across lines, and `[a [b]](x)` are invisible to
LINK. Every one of these fails toward a false broken target rather than a missed real
one, except the reference-style form which is simply unseen.
"""

import pathlib
import re
import sys

# Matches scripts/docscan/main.go's excludedDir, which skips these same two.
SKIP_DIRS = {".git", "dist"}
LINK = re.compile(r"\[[^\]]+\]\(([^)]+)\)")
FENCE = re.compile(r"^\s*(?:```|~~~)")
CODE_SPAN = re.compile(r"(`+)[^`]*?\1")

generated = sys.argv[1] if len(sys.argv) > 1 else ""
root = pathlib.Path.cwd()

for path in sorted(root.rglob("*.md")):
    rel = path.relative_to(root).as_posix()
    if any(part in SKIP_DIRS for part in path.relative_to(root).parts):
        continue
    # Anchored with a trailing slash, the way docscan/main.go's generatedExport is:
    # a bare prefix test would also swallow a sibling like "…/go-cli-tooling-extra/".
    # The same substring class this repository keeps paying for.
    if generated and (rel + "/").startswith(generated.rstrip("/") + "/"):
        continue
    fenced = False
    for line in path.read_text(errors="replace").splitlines():
        if FENCE.match(line):
            fenced = not fenced
            continue
        if fenced:
            continue
        for target in LINK.findall(CODE_SPAN.sub("", line)):
            print(f"{rel}\t{target}")
