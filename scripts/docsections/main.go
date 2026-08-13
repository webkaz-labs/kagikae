// Command docsections emits every `<file>.md § <name>` citation as
// `<citing>\t<target>\t<verdict>\t<name>`, for check-docs.sh. Verdict is `resolves`,
// `absent`, or `external`.
//
// The defect this exists for: a citation naming a section its target has never had. One
// shipped — docs/ROADMAP.md cited "§ Tier-1 tools", a section that file has never had,
// and a reviewer found it. Nothing in this repository's gate read the sigil before this
// program.
//
// Go rather than the Python this was first written in. scripts/docs_links.py is the only
// Python in the tree, mise.toml declares no python, and CI runs neither docs-check nor
// anything else that would notice a machine without python3 — so a second Python script
// deepens an undeclared dependency. scripts/docscan is the same shape, and the release is
// unaffected because .goreleaser.yaml builds `main: .`.
//
// # The predicate
//
// It is a prefix test on the words after the sigil, and it deliberately does not try
// to find where the cited name ends. A citation here quotes a *distinctive leading
// fragment* of the name, not the whole name — `docs/CLI.md § kae pin` cites
// `## kae pin and mise init Semantics` — so there is no terminator to find. Three
// designs that tried anyway were measured on this tree first, and each would have
// failed the gate on correct prose:
//
//   - terminate the name at the first structural character: 21 false positives, on
//     quoted names, bold names, code-span names and table cells;
//   - require the citation to *start with* a whole section name: 71, because the idiom
//     shortens the name;
//   - require the first three words to appear anywhere in the target: 59, because most
//     section names here are one or two words and the third belongs to the sentence.
//
// Each count is out of a different total (154, 224, 191) because each of those designs
// also changed which citations were extracted, so the three are comparable as verdicts
// on a design and not as a series. This form fails nothing on a correct tree.
//
// # Two exclusions, both measured rather than guessed
//
//   - The sigil must be followed by a space and then a letter, a code span or
//     emphasis. That drops `§6`, `§7`, `§4.1` (numbered sections of
//     docs/SCOPE-MODEL.md) and `§A`, `§A/§C` (docs/RELEASE.md sub-sections), which
//     introduce no searchable name. Note which half does the work: every live instance
//     is written with no space at all (measured: zero `§ <digit>`, twenty-one
//     `§<digit>`), so widening the character class to admit digits would change
//     nothing.
//   - A fenced block is stripped, on both sides. AGENTS.md documents the citation
//     forms themselves, and an unstripped run reported its illustrations as broken
//     targets — the upstream template check's own open defect, reproduced. On the name
//     side the strip has to be line-anchored; stripFences says what that cost when it
//     was not.
//
// # A measured cost, not paid down
//
// `citeRe` opens with a `*` quantifier, so RE2 has no literal prefix to accelerate on and
// scans all ~2MB of joined text byte by byte: 107ms of this program's 216ms, its largest
// single item, and check-docs.sh runs 20 times per `mise run check`. Anchoring the scan on
// the `§` literal instead — which RE2 does accelerate — and matching the filename half
// backwards in a bounded window was measured at 136ms saved, output identical on this tree
// and all selftest cases holding. It is not done here because it changes *how* citations
// are found (non-overlapping forward matches become per-sigil lookback) and the
// equivalence is measured rather than proved, which is not a trade to make in the same
// pass that found two defects in this matcher. The saving and the caveat are both real;
// take it with its own review.
//
// # The ceilings, so a clean run is not read as more than it is
//
//   - Only the *first word* of the cited name is compared. `§ Tier-1 tools` is caught
//     because no name in that file begins `Tier-1`; `§ Open gate` for `Open gates` is
//     not. A two-word form was measured at three failures, and all three are correct
//     prose: the idiom lets a citation name a *one-word* fragment of a multi-word
//     heading (`§ Cursor` for `## Cursor CLI (cursor-agent)`) or an *interior* one
//     (`§ kae relogin and credential_superseded` for `## v0.17.0 surface — kae relogin
//     and credential_superseded`). The interior form is why widening is not just a
//     stricter number: a first-word test passes it by matching some other section that
//     happens to share that word, so this walk confirms those citations for the wrong
//     reason. Verifying them means matching a fragment anywhere in a name, which is a
//     different predicate, not a tighter one.
//   - Only markdown and Go are read, per suffixes below.
//   - Only the `X.md §` form is read. The bare `§ Name` form is this repository's
//     dominant idiom — AGENTS.md's routing lines are all bare — and it names no file, so
//     nothing here can resolve it. A `§` count over the tree far exceeds this program's
//     row count and the gap is the bare form; both numbers move with every edit, so
//     derive them rather than reading one here. The first draft of this bullet wrote
//     both, and the commit that wrote them falsified them.
//   - A target that resolves nowhere is `external`, which check-docs.sh skips without
//     counting, so it can never fail. A citation naming a file that does not exist is
//     therefore silent here; the link walk catches it only when it is written as a
//     markdown link, and most citations in Go comments are not.
package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Matches scripts/docs_links.py, and for a measured reason rather than for symmetry:
// enumerating with `git ls-files` was the first form here and it fails inside the
// selftest's fixture, which is a plain copy of the tracked tree and not a repository.
var skipDirs = map[string]bool{".git": true, "dist": true}

// Two types, and the second reason is the one that constrains this rather than the
// first. Citations live in markdown and in Go comments today — that is measured. But a
// walk over every text file also reads scripts/check-docs-selftest.sh, whose fixture
// for the phantom-citation case *is* a broken citation written as a shell string, so
// the check failed on its own test. scripts/docs_links.py never meets that because it
// globs `*.md`; this is the same answer for the same reason. The ceiling: a citation
// added to a shell or Python file is not checked, and nothing reports that. Widening
// the set means moving the selftest's fixtures somewhere the walk does not reach first.
var suffixes = []string{".md", ".go"}

var (
	// The name window is bounded so a citation cannot claim the rest of the file, and
	// the bound is applied by slicing the tail rather than by a capture group: as a
	// consuming capture it swallowed any second citation within the window — one joined
	// line here, so across line breaks — and five live citations went unemitted while a
	// phantom written beside a valid citation passed.
	citeRe    = regexp.MustCompile("([A-Za-z0-9_./-]*\\.md)[`'\")\\]]*[ \t]*§[ \t]+[A-Za-z`\"*_]")
	headingRe = regexp.MustCompile(`^#{1,6}\s+(.*)$`)
	// A bold label that opens a line, optionally struck through; a list-item title is
	// read by listLabelRe instead, and carrying the marker here too was measured to
	// change nothing. Anchored for the reason sectionNames states.
	labelRe     = regexp.MustCompile(`^\s*(?:~~)?\*\*(.+?)\*\*`)
	listLabelRe = regexp.MustCompile(`[-*]\s+(?:~~)?\*\*(.+?)\*\*`)
	// Leading whitespace and `~~~`, matching scripts/docs_links.py's FENCE rather than
	// inventing a third dialect — three copies of this model had three different ones,
	// and this was the strictest. It cost both halves of the strip on the one file whose
	// only fenced block is indented: docs/RELEASE.md has two indented fences and no
	// column-0 fence, so a `§` illustration inside it was emitted as `absent` (the gate
	// failing on a code block) and a bold label inside it became a section name a
	// phantom resolved against. The `~~~` half has no instance today and is here only
	// because matching the sibling's dialect is what stops the next divergence.
	fenceRe      = regexp.MustCompile("(?ms)^[ \t]*(?:```|~~~).*?^[ \t]*(?:```|~~~)[^\n]*\n?")
	commentRe    = regexp.MustCompile(`^\s*(?://+|#+)\s*`)
	decorationRe = regexp.MustCompile("[`*_\"'()\\[\\]|~]")
	trailingRe   = regexp.MustCompile(`[.,;:]+$`)
)

// nameWindow bounds how much text after the sigil is read as the cited name. Only its
// first word decides the verdict; the rest is carried so a diagnostic can quote enough
// of the citation to find it.
const nameWindow = 90

// unwrap joins every line, stripping a leading comment marker from continuations so a
// citation or a name that wrapped reads as one string.
func unwrap(text string) string {
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t\r")
		if i > 0 {
			lines[i] = commentRe.ReplaceAllString(lines[i], " ")
		}
	}
	return strings.Join(lines, " ")
}

// words compares on words only: emphasis, backticks, quotes and trailing punctuation
// are decoration a citation is free to differ on.
func words(text string) []string {
	text = decorationRe.ReplaceAllString(text, " ")
	out := []string{}
	for _, w := range strings.Fields(strings.ToLower(text)) {
		w = trailingRe.ReplaceAllString(w, "")
		if w != "" {
			out = append(out, w)
		}
	}
	return out
}

// stripFences is line-anchored, because a heading is matched per line: an unanchored
// strip leaves the fence markers' own lines behind. Measured before this was anchored:
// 254 heading-shaped lines live inside fenced blocks in this tree — shell comments in
// docs/VALIDATION.md's runnable blocks, mostly — and every one was accepted as a
// section name, so `§ Canonical smoke ordering` resolved against a `# canonical …`
// comment.
func stripFences(text string) string {
	// The guard is not a micro-optimisation dressed up: `fenceRe` is `(?ms)` with a lazy
	// `.*?`, so it costs about 20ns/byte even where nothing can match, and only 15 of the
	// 69 sigil-bearing files hold a fence marker at all. Measured at 41ms of this
	// program's 216ms — and check-docs.sh runs 20 times per `mise run check`, once for
	// itself and once per selftest fixture. It cannot change the result: the pattern
	// requires one of these two literals.
	if !strings.Contains(text, "```") && !strings.Contains(text, "~~~") {
		return text
	}
	return fenceRe.ReplaceAllString(text, "")
}

// sectionNames returns headings, list-item bold titles, and a bold label that opens a
// line.
//
// The third form is deliberately narrow. Accepting every bold run over the joined text
// — which is what this did first — accepts mid-sentence emphasis, and this tree writes
// a great deal of it: on docs/ROADMAP.md that produced 120 accepted first-words where
// headings and list titles give 39, the surplus including "and", "is", "it", "both",
// "before", "copy" and "done", so `§ Both open gates` resolved against the word *both*
// in a sentence.
func sectionNames(markdown string) [][]string {
	stripped := stripFences(markdown)
	var names [][]string
	for _, line := range strings.Split(stripped, "\n") {
		if m := headingRe.FindStringSubmatch(line); m != nil {
			names = append(names, words(m[1]))
		}
		if m := labelRe.FindStringSubmatch(line); m != nil {
			names = append(names, words(m[1]))
		}
	}
	// A list title or a label wraps, and the name is then split across two lines, so
	// the same form is read again from the joined text — anchored on the list marker,
	// which mid-sentence emphasis does not have.
	for _, m := range listLabelRe.FindAllStringSubmatch(unwrap(stripped), -1) {
		names = append(names, words(m[1]))
	}
	kept := names[:0]
	for _, n := range names {
		if len(n) > 0 {
			kept = append(kept, n)
		}
	}
	return kept
}

// firstWordMatches reports whether any declared name begins with the cited name's
// first word. See the ceilings in the package comment for why only the first.
func firstWordMatches(names [][]string, cited []string) bool {
	if len(cited) == 0 {
		return false
	}
	for _, n := range names {
		if len(n) > 0 && n[0] == cited[0] {
			return true
		}
	}
	return false
}

// resolveTarget tries the citation's own directory, then the repository root. Both are
// conventions in use: relative-to-the-citing-file in docs/, and repo-root in Go comments.
//
// A third candidate, root/docs/<basename>, was here and is deliberately gone. It served
// exactly one citation — AGENTS.md quoting an old docs/ROADMAP.md sentence as a bare
// `CONTEXT.md` — and in exchange it resolved any wrong directory with a right basename:
// `doc/CLI.md` and even `../../nonsense/ADAPTERS.md` were reported `resolves` and counted,
// so the gate called a typo checked and correct. The quotation was normalised to
// `docs/CONTEXT.md` instead, which is where a one-instance convention belongs.
func resolveTarget(root, citing, target string) string {
	for _, candidate := range []string{
		filepath.Join(root, filepath.Dir(citing), target),
		filepath.Join(root, target),
	} {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate
		}
	}
	return ""
}

func hasSuffix(name string) bool {
	for _, s := range suffixes {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "docsections: %v\n", err)
		os.Exit(1)
	}

	var files []string
	err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if hasSuffix(d.Name()) {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "docsections: walking %s: %v\n", abs, err)
		os.Exit(1)
	}
	sort.Strings(files)

	cache := map[string][][]string{}
	namesFor := func(p string) [][]string {
		if n, ok := cache[p]; ok {
			return n
		}
		body, err := os.ReadFile(p)
		if err != nil {
			cache[p] = nil
			return nil
		}
		n := sectionNames(string(body))
		cache[p] = n
		return n
	}

	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	for _, p := range files {
		body, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		text := string(body)
		if !strings.Contains(text, "§") {
			continue
		}
		rel, err := filepath.Rel(abs, p)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		joined := unwrap(stripFences(text))
		for _, loc := range citeRe.FindAllStringSubmatchIndex(joined, -1) {
			target := joined[loc[2]:loc[3]]
			// The match's last character is the cited name's first, because the pattern
			// consumes one character of the name to assert its shape.
			tail := joined[loc[1]-1:]
			if len(tail) > nameWindow {
				tail = tail[:nameWindow]
			}
			cited := words(tail)
			if len(cited) == 0 {
				continue
			}
			shown := strings.Join(cited[:min(4, len(cited))], " ")
			resolved := resolveTarget(abs, rel, target)
			if resolved == "" || !strings.HasSuffix(resolved, ".md") {
				fmt.Fprintf(out, "%s\t%s\texternal\t%s\n", rel, target, shown)
				continue
			}
			verdict := "absent"
			if firstWordMatches(namesFor(resolved), cited) {
				verdict = "resolves"
			}
			fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", rel, target, verdict, shown)
		}
	}
}
