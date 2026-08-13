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

  * terminate the name at the first structural character: 21 false positives, on quoted
    names, `**bold**` names, code-span names and table cells;
  * require the citation to *start with* a whole section name: 71, because the idiom
    shortens the name;
  * require the first three words to appear anywhere in the target: 59, because most
    section names here are one or two words and the third belongs to the sentence.

Each count is out of a different total (154, 224, 191) because each of those designs
also changed which citations were extracted, so the three are comparable as verdicts on
a design and not as a series. The form below fails nothing on a correct tree, and catches
a planted `§ Tier-1 tools` — the citation this repository actually shipped.

Two exclusions, both measured rather than guessed:

  * the sigil must be followed by a **space** and then a letter, a code span or
    emphasis. That drops `§6`, `§7`, `§4.1` (numbered sections of docs/SCOPE-MODEL.md)
    and `§A`, `§A/§C` (docs/RELEASE.md sub-sections), which introduce no searchable
    name. Note which half does the work: every live instance is written with no space
    at all, so widening the character class to admit digits would change nothing.
  * a fenced block is stripped, on both sides. `AGENTS.md` documents the citation forms
    themselves, and an unstripped run reported its illustrations as broken targets —
    the upstream template check's own open defect, reproduced. On the name side the
    strip has to be line-anchored; `strip_fences` says what that cost when it was not.

**Four ceilings, so a clean run is not read as more than it is.**

  * Only the **first word** of the cited name is compared. `§ Tier-1 tools` is caught
    because no name in that file begins `Tier-1`; `§ Open gate` for `Open gates` is not.
    The two-word form was measured at 3 failures here, one a citation at the end of a Go
    comment whose text runs into the code beneath it — a correct file. Widening it means
    handling that case first.
  * Only markdown and Go are read, per SUFFIXES below.
  * Only the `X.md §` form is read. The **bare `§ Name`** form is this repository's
    dominant idiom — AGENTS.md's routing lines are all bare — and it names no file, so
    nothing here can resolve it. Measured: the tree holds 514 sigils and this emits
    under 200 rows. The bare form is what `AGENTS.md § Documentation Update Checklist`
    still asks for by hand.
  * A target that resolves nowhere is `external`, not a failure, and `check-docs.sh`
    does not count it — so a typo'd *directory* (`doc/CLI.md`) is reported by neither
    half of the gate. `resolve()` says why the third candidate path exists.

Section names are `#` headings, list-item bold titles, and a bold label that opens a
line; `section_names` says why the third is anchored rather than taken from anywhere.
Names and citations both wrap, so the text is joined first, with a leading comment
marker stripped from continuations.
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

# The sigil must be followed by a space and then a letter, a code span or emphasis; see
# the exclusions in the module docstring.
#
# The trailing window is a **lookahead**, so it consumes nothing. As a consuming capture it
# swallowed any second citation within 90 characters of the first — `finditer` resumes at
# the end of a match, and the whole file is one joined line here, so the window spans lines.
# Measured: five live citations were never emitted, and a phantom written next to a valid
# citation passed the gate.
CITE = re.compile(r"([A-Za-z0-9_./-]*\.md)[`'\")\]]*\s*§\s+(?=[A-Za-z`\"*_])(?=(.{1,90}))")
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


def strip_fences(text):
    """Line-anchored, because a heading is matched per line: an unanchored strip leaves
    the fence markers' own lines behind. Measured before this was anchored: 254
    heading-shaped lines live inside fenced blocks in this tree — shell comments in
    docs/VALIDATION.md's runnable blocks, mostly — and every one of them was being
    accepted as a section name, so `§ Canonical smoke ordering` resolved against a
    `# canonical …` comment."""
    return re.sub(r"(?ms)^```.*?^```[^\n]*\n?", "", text)


def section_names(markdown):
    """Headings, list-item bold titles, and a bold label that opens a line.

    The third form is deliberately narrow. Accepting every `**…**` run over the joined
    text — which is what this did first — accepts mid-sentence emphasis, and this tree
    writes a great deal of it: on docs/ROADMAP.md that produced 120 accepted first-words
    where headings and list titles give 39, the surplus including `and`, `is`, `it`,
    `both`, `before`, `copy` and `done`. `§ Both open gates` then resolved against the
    word *both* in a sentence."""
    stripped = strip_fences(markdown)
    names = []
    for line in stripped.split("\n"):
        heading = re.match(r"^#{1,6}\s+(.*)$", line)
        if heading:
            names.append(words(heading.group(1)))
        label = re.match(r"^\s*(?:[-*]\s+)?(?:~~)?\*\*(.+?)\*\*", line)
        if label:
            names.append(words(label.group(1)))
    # A list title or a label wraps, and the name is then split across two lines, so the
    # same two forms are read again from the joined text — anchored on the list marker,
    # which mid-sentence emphasis does not have.
    joined = unwrap(stripped)
    for m in re.finditer(r"[-*]\s+(?:~~)?\*\*(.+?)\*\*", joined):
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
        joined = unwrap(strip_fences(text))
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
