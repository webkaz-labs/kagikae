#!/usr/bin/env python3
"""Emit every markdown link in the tree as `<file>\t<target>`, for check-docs.sh.

Why this is Python and not another line in that shell script: AGENTS.md's own rule
says to use python3 for regex extraction you intend to count, because BSD `grep -oE`
can exit 1 on a file that does contain matches. Two further shell traps were hit
writing this the other way, both already recorded in this repository:

  * `grep` exits 1 on a markdown file with no links, which under `set -e` with
    `pipefail` kills the producer and leaves the walk reporting zero links found;
  * an apostrophe inside a comment (`AGENTS.md's`) sitting inside a process
    substitution opens a single-quoted string and mangles everything after it.
    `bash -n` and `shellcheck` both pass on that; it only fails at run time.

A link-shaped string inside code is an example, not a link, so fenced blocks and
inline code spans are removed first. AGENTS.md's citation rule contains a bracketed
`X.md` pair as an illustration of a grep form, and the first real run of the check
reported it as a broken target.

Known gap, measured as not currently reachable: CODE_SPAN handles single-backtick
spans, so markdown's double-backtick escaping idiom is stripped as an outer pair plus
a separate inner span, leaving any text between them exposed to LINK. No document here
puts link syntax in that position today, and the check would report a false broken
target rather than miss a real one, so this is disclosed rather than closed.
Reference-style links (`[x][1]` with a separate `[1]: path` definition) are also
invisible to LINK; there are none in this repository.
"""

import pathlib
import re
import sys

SKIP_DIRS = {".git"}
LINK = re.compile(r"\[[^\]]+\]\(([^)]+)\)")
FENCE = re.compile(r"^\s*```")
CODE_SPAN = re.compile(r"`[^`]*`")

generated = sys.argv[1] if len(sys.argv) > 1 else ""
root = pathlib.Path.cwd()

for path in sorted(root.rglob("*.md")):
    rel = path.relative_to(root).as_posix()
    if any(part in SKIP_DIRS for part in path.relative_to(root).parts):
        continue
    if generated and rel.startswith(generated):
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
