#!/usr/bin/env python3
"""Emit every `<file>.md § <name>` citation as `<citing>\t<target>\t<verdict>\t<name>`,
for check-docs.sh. Verdict is `resolves`, `absent`, or `external`.

The defect this exists for: a citation naming a section its target has never had.
`AGENTS.md` § Documentation Update Checklist asks for that by hand ("grep the name,
not the sigil"), and one shipped anyway — `docs/ROADMAP.md` cited "§ Tier-1 tools",
a section that file has never had, and it was found by a reviewer rather than by
anything here. Nothing in this repository's gate reads the sigil: before this file,
`grep -n '§' scripts/check-docs.sh scripts/docs_links.py` found one hit, in a comment.

**The predicate is a prefix test on the words after the sigil, and it deliberately
does not try to find where the cited name ends.** A citation here quotes a
*distinctive leading fragment* of the name, not the whole name — `docs/CLI.md §
kae pin` cites `## kae pin and mise init Semantics` — so there is no terminator to
find. Three designs that tried anyway were measured on this tree first, and each
would have failed the gate on correct prose:

  * terminate the name at the first structural character: 21 false positives of 154,
    on quoted names, `**bold**` names, code-span names and table cells;
  * require the citation to *start with* a whole section name: 71 of 224, because the
    idiom shortens the name;
  * require the first three words to appear anywhere in the target: 59 of 191,
    because most section names here are one or two words and the third word belongs
    to the sentence, not the name.

The form below is 0 of 191 on this tree, and catches a planted `§ Tier-1 tools`.

Three exclusions, each measured rather than guessed:

  * the sigil must be followed by a space and then a letter. `§A`, `§A/§C` (release
    sub-sections) and `§ 6`, `§ 7` (numbered sections of docs/SCOPE-MODEL.md) do not
    introduce a searchable name. Excluded by shape, not by a word list, because a
    word list here would go short the way every enumeration in this tree has.
  * a target that is not a tracked `.md` file is `external`: the shared Go CLI
    standard lives outside this repository and its sections cannot be resolved from
    here (AGENTS.md's opening says why the copy went away).
  * a fenced block is stripped. `AGENTS.md` documents the citation forms themselves,
    and the first honest run reported its illustrations as broken targets — which is
    exactly the upstream template check's own open defect, reproduced.

**Its ceiling, so a clean run is not read as more than it is: only the first word of
the cited name is compared.** `§ Tier-1 tools` is caught because no section name in
that file begins `Tier-1`; `§ Open gate` for `Open gates` would not be. The stronger
two-word form was measured at 3 failures on this tree, one of them a citation at the
end of a Go comment whose text runs on into the code beneath it, so the gate would
fail on a correct file. Widening this is worth doing only with that case handled.

Section names are `#` headings, list-item bold titles and bold labels, because all
three are cited by `§` in this tree (`§ Every credential copy` is a bullet title,
`§ Tool Tiers` is a heading). Names and citations both wrap across lines, so the
text is joined first, with a leading comment marker stripped from continuations.
"""
import re
import sys
from pathlib import Path

# Matches scripts/docs_links.py, and for a measured reason rather than for symmetry:
# `git ls-files` was the first enumeration here and it exits 128 inside the selftest's
# fixture, which is a plain copy of the tracked tree and not a repository. Every
# fixture-based case failed with a traceback, which is how this was found.
SKIP_DIRS = {".git", "dist"}

# **Two types, and the second reason is the one that constrains this rather than the
# first.** Citations live in markdown and in Go comments today — that is measured. But a
# walk over every text file also reads `scripts/check-docs-selftest.sh`, whose fixture for
# the phantom-citation case *is* a broken citation written as a shell string, so the check
# failed on its own test. scripts/docs_links.py never meets that because it globs `*.md`;
# this is the same answer for the same reason. **The ceiling: a citation added to a shell
# or Python file is not checked, and nothing reports that.** Widening the set means moving
# the selftest's fixtures somewhere the walk does not reach first.
SUFFIXES = (".md", ".go")

# The sigil must be followed by a space and then a letter or an opening quote; see
# the exclusions in the module docstring.
CITE = re.compile(r"([A-Za-z0-9_./-]*\.md)[`'\")\]]*\s*§\s+(?=[A-Za-z`\"])(.{1,90})")
DECORATION = re.compile(r"[`*_\"'()\[\]|~]")
TRAILING_PUNCT = re.compile(r"[.,;:]+(?=\s|$)")


def unwrap(text):
    """Join every line, stripping a leading comment marker from continuations so a
    citation or a name that wrapped reads as one string."""
    out = []
    for i, line in enumerate(text.split("\n")):
        stripped = line.rstrip()
        if i:
            stripped = re.sub(r"^\s*(?://+|#+)\s*", " ", stripped)
        out.append(stripped)
    return " ".join(out)


def words(text):
    """Compare on words only: emphasis, backticks, quotes and trailing punctuation
    are decoration a citation is free to differ on."""
    text = DECORATION.sub(" ", text)
    text = TRAILING_PUNCT.sub(" ", text)
    return [w for w in re.split(r"\s+", text.lower()) if w]


def section_names(markdown):
    names = []
    for line in markdown.split("\n"):
        heading = re.match(r"^#{1,6}\s+(.*)$", line)
        if heading:
            names.append(words(heading.group(1)))
    joined = unwrap(markdown)
    for m in re.finditer(r"[-*]\s+(?:~~)?\*\*(.+?)\*\*", joined):
        names.append(words(m.group(1)))
    for m in re.finditer(r"\*\*(.+?)\*\*", joined):
        names.append(words(m.group(1)))
    return [n for n in names if n]


def main():
    root = Path(sys.argv[1] if len(sys.argv) > 1 else ".").resolve()
    cache = {}

    def names_for(path):
        if path not in cache:
            cache[path] = section_names(path.read_text(encoding="utf-8"))
        return cache[path]

    def resolve(citing, target):
        for candidate in (
            (root / citing).parent / target,
            root / target,
            root / "docs" / Path(target).name,
        ):
            if candidate.is_file():
                return candidate
        return None

    for path in sorted(p for s in SUFFIXES for p in root.rglob(f"*{s}")):
        if not path.is_file():
            continue
        rel_parts = path.relative_to(root).parts
        if any(part in SKIP_DIRS for part in rel_parts):
            continue
        rel = path.relative_to(root).as_posix()
        try:
            text = path.read_text(encoding="utf-8")
        except (UnicodeDecodeError, OSError):
            continue
        if "§" not in text:
            continue
        joined = unwrap(re.sub(r"```.*?```", " ", text, flags=re.S))
        for m in CITE.finditer(joined):
            target, tail = m.group(1), m.group(2)
            cited = words(tail)
            if not cited:
                continue
            shown = " ".join(cited[:4])
            resolved = resolve(rel, target)
            if resolved is None or resolved.suffix != ".md":
                print(f"{rel}\t{target}\texternal\t{shown}")
                continue
            first = cited[:1]
            if any(name[: len(first)] == first for name in names_for(resolved)):
                print(f"{rel}\t{target}\tresolves\t{shown}")
            else:
                print(f"{rel}\t{target}\tabsent\t{shown}")


if __name__ == "__main__":
    main()
