// Command docrefs emits every reference from one document into another, one per line,
// for check-docs.sh:
//
//	link	<citing>	<target>
//	cite	<citing>	<target>	<verdict>	<name>
//
// Verdict is `resolves`, `absent`, or `external`. A link row carries no verdict on
// purpose: resolving one is three lines of parameter expansion in the caller, which is
// already where the `-f` narrowing, the fragment strip and the scheme/absolute skips
// live.
//
// # The defects this exists for
//
// A markdown link whose target does not exist, and a citation naming a section its
// target has never had. The second shipped: docs/ROADMAP.md cited "§ Tier-1 tools", a
// section that file has never had, and a reviewer found it. Nothing in this
// repository's gate read the sigil before this program.
//
// # One program, two extractors, and why they are not one extractor
//
// A link and a `§` citation are both a reference from one document into another, so the
// walk, the pruned directories, the fence dialect and the runtime are shared here — that
// duplication used to sit in two languages. The extraction is not shared and cannot be:
// code-span stripping is required for links and forbidden for names, which are free to
// wrap the cited name in backticks; and line-joining is required for citations, which
// wrap, and fatal for links, which do not — a `](` pair formed across a line break is not
// a link. Two functions in one file is what that argues for; one function is what it
// forbids.
//
// The two fence models also stay separate, for a measured difference rather than for
// symmetry: stripFences requires a closing fence, so an unclosed one leaves the rest of
// the file readable, while the link half toggles per line and treats everything after an
// unclosed fence as fenced. Only the dialect is shared — leading whitespace, and `~~~`
// as well as three backticks — and the dialect is the half that had drifted three ways.
//
// # Go rather than the Python the link half was first written in
//
// mise.toml declares no python and CI runs neither docs-check nor docs-check-selftest, so
// a python3 the gate needs is a dependency nothing declares and nothing outside a
// developer's machine would notice. Derive what is left rather than reading a count here:
// `grep -rn python3 scripts/`. Measured before this port, with a python3 that exits 127:
// check-docs.sh was loud but misdiagnosing, reporting `the link extractor exited
// non-zero` about an interpreter that was never there, and check-docs-selftest.sh failed
// its first two cases for a reason neither case tests and then exited 127 inside its
// third with nothing but `command not found`. This program is also inside `go vet`,
// `golangci-lint` and `go test`, which is where main_test.go's cases live; scripts/docscan
// is the same shape, and the release is unaffected because .goreleaser.yaml builds
// `main: .`.
//
// # The link predicate, and what it cannot see
//
// A link-shaped string inside code is an example, not a link, so fenced blocks and inline
// code spans are removed first. AGENTS.md's citation rule contains a bracketed `X.md`
// pair as an illustration of a grep form, and the first real run of this check reported it
// as a broken target.
//
// The ceilings, each measured as having no instance in this tree today. A tilde fence is
// tracked as well as a backtick one, but a four-space indented code block is not, so a
// link inside one is reported as broken. Reference-style links (`[x][1]` with a separate
// `[1]: path` definition), links split across lines, and `[a [b]](x)` are invisible to
// linkRe. Every one of these fails toward a false broken target rather than a missed real
// one, except the reference-style form, which is simply unseen.
//
// # The citation predicate
//
// It is a prefix test on the words after the sigil, and it deliberately does not try
// to find where the cited name ends. A citation here quotes a *distinctive leading
// fragment* of the name, not the whole name — a citation reading
// `docs/ZZEXAMPLE.md § kae pin` would name `## kae pin and mise init Semantics` — so
// there is no terminator to find. The illustration names a file that does not exist on
// purpose: this program reads `.go`, so a resolvable citation in its own comment counts
// toward check-docs.sh's md/go guard, and while it did, pruning `internal` stopped 55
// citations from being checked and the gate still reported ok. Three
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
//     is written with no space at all — `git grep -o '§ [0-9]' -- '*.md' '*.go'` is
//     empty while `§[0-9]` is not — so widening the character class to admit digits
//     would change nothing.
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
// single item, and check-docs.sh runs once for the gate plus once per selftest case, so
// the cost is paid that many times per `mise run check`. Anchoring the scan on
// the `§` literal instead — which RE2 does accelerate — and matching the filename half
// backwards in a bounded window was measured at 136ms saved, output identical on this tree
// and all selftest cases holding. It is not done here because it changes *how* citations
// are found (non-overlapping forward matches become per-sigil lookback) and the
// equivalence is measured rather than proved, which is not a trade to make in the same
// pass that found two defects in this matcher. The saving and the caveat are both real;
// take it with its own review.
//
// # The citation ceilings, so a clean run is not read as more than it is
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
//   - Only markdown and Go are read, per suffixes below; the link half reads markdown
//     only, because a `](` pair in Go is not a link.
//   - A cited name whose first character is not ASCII is not extracted at all (`§
//     Übersicht`, `§ “quoted`), and nameWindow bounds the diagnostic in bytes, so a
//     window ending mid-rune renders as U+FFFD in the message rather than truncating
//     cleanly. Neither has an instance today; both are inherited from the form this
//     replaced.
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

// Both halves prune the same two, which is one of the things merging them bought. Not a
// `git ls-files` enumeration, for a measured reason: that was the first form here and it
// fails inside the selftest's fixture, which is a plain copy of the tracked tree and not a
// repository.
var skipDirs = map[string]bool{".git": true, "dist": true}

// Two types, and the second reason is the one that constrains this rather than the
// first. Citations live in markdown and in Go comments today — that is measured. But a
// walk over every text file also reads scripts/check-docs-selftest.sh, whose fixture
// for the phantom-citation case *is* a broken citation written as a shell string, so
// the check failed on its own test. The link half never meets that, because it reads the
// `.md` subset of this same walk; this is the same answer for the same reason. The
// ceiling: a citation added to a shell script is not checked, and nothing reports that.
// Widening the set means moving the selftest's fixtures somewhere the walk does not reach
// first.
var suffixes = []string{".md", ".go"}

var (
	// The name window is bounded so a citation cannot claim the rest of the file, and
	// the bound is applied by slicing the tail rather than by a capture group: as a
	// consuming capture it swallowed any second citation within the window — one joined
	// line here, so across line breaks — so live citations went unemitted (six when it was
	// measured; derive it rather than trusting that) while a phantom written beside a
	// valid citation passed.
	citeRe    = regexp.MustCompile("([A-Za-z0-9_./-]*\\.md)[`'\")\\]]*[ \t]*§[ \t]+[A-Za-z`\"*_]")
	headingRe = regexp.MustCompile(`^#{1,6}\s+(.*)$`)
	// A bold label that opens a line, optionally struck through; a list-item title is
	// read by listLabelRe instead, and carrying the marker here too was measured to
	// change nothing. Anchored for the reason sectionNames states.
	labelRe     = regexp.MustCompile(`^\s*(?:~~)?\*\*(.+?)\*\*`)
	listLabelRe = regexp.MustCompile(`[-*]\s+(?:~~)?\*\*(.+?)\*\*`)
	// Leading whitespace and `~~~`, the dialect fenceLineRe below shares. Measured
	// honestly: reverting this to the column-0 form leaves today's output byte-identical,
	// because the tree's only indented fence — docs/RELEASE.md, which has no column-0
	// fence at all — happens to contain neither a `§` nor a `**`. So this is dialect
	// alignment plus what selftest case 18 guarantees, not a live defect repaired; the
	// column-0 form fails that case and nothing else. Three copies of this model had
	// three dialects; merging the two here leaves two copies of the model and one dialect,
	// and scripts/docscan/main.go still carries the third (no `~~~`). That was the argument
	// for matching a sibling rather than for the strictness.
	fenceRe = regexp.MustCompile("(?ms)^[ \t]*(?:```|~~~).*?^[ \t]*(?:```|~~~)[^\n]*\n?")
	// The link half's fence, one line apart from the block form above so the dialect
	// cannot drift again, and deliberately a different model: see the package comment.
	fenceLineRe  = regexp.MustCompile("^[ \t]*(?:```|~~~)")
	linkRe       = regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)
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

// stripCodeSpans removes inline code spans: a run of backticks opens a span that the
// next run of at least the same length closes, and the content between them holds no
// backtick at all.
//
// The run length is the load-bearing half. A rule that only knew single backticks left
// the two-backtick idiom exposed: given a link wrapped in a pair on each side, it ate the
// opening pair as an empty span and then the closing pair, handing the link straight to
// linkRe. AGENTS.md uses that idiom, and it is what you reach for when a span must itself
// contain a backtick, which is the shape its own citation rule discusses. Since this runs
// in `mise run check`, the symptom was a gate that blocks a commit on correct prose. One
// example does not pin a character class, so main_test.go carries the shapes — and it
// carries them because they were measured against the Python this replaced, not read off
// this code.
//
// Written by hand because RE2 has no backreference, so the rule the Python expressed as a
// captured backtick run required again to close does not compile here. The one
// behavioural difference that survived is in the bytes and not in the outcome: where that
// pattern could backtrack to a shorter opening run and consume part of it — three
// backticks with nothing to close against left one behind — this emits the whole run
// literally. Both then hand the same text to linkRe, which is what main_test.go's shapes
// and this tree's byte-for-byte output between the two implementations say.
func stripCodeSpans(line string) string {
	var b strings.Builder
	for i := 0; i < len(line); {
		if line[i] != '`' {
			b.WriteByte(line[i])
			i++
			continue
		}
		open := i
		for i < len(line) && line[i] == '`' {
			i++
		}
		run := i - open
		// The first backtick after the content is the only candidate close, because the
		// content may not contain one.
		next := strings.IndexByte(line[i:], '`')
		if next < 0 {
			b.WriteString(line[open:i])
			continue
		}
		closeAt := i + next
		closeRun := 0
		for closeAt+closeRun < len(line) && line[closeAt+closeRun] == '`' {
			closeRun++
		}
		if closeRun < run {
			b.WriteString(line[open:i])
			continue
		}
		// The span is dropped, and any backticks past the ones this close consumed stay:
		// they are free to open the next span.
		i = closeAt + run
	}
	return b.String()
}

// extractLinks returns every relative-or-absolute markdown link target in document order.
//
// Per line, and per line for a reason: joining first would form a `](` pair across a line
// break and report a link nobody wrote. The fence state carries across lines, which is
// the only thing that does.
func extractLinks(markdown string) []string {
	var targets []string
	fenced := false
	for _, line := range strings.Split(markdown, "\n") {
		if fenceLineRe.MatchString(line) {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		for _, m := range linkRe.FindAllStringSubmatch(stripCodeSpans(line), -1) {
			targets = append(targets, m[1])
		}
	}
	return targets
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
// a great deal of it: on docs/ROADMAP.md the unanchored form accepts roughly three times
// the first-words headings and list titles give, the surplus including "and", "is", "both",
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
		fmt.Fprintf(os.Stderr, "docrefs: %v\n", err)
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
		fmt.Fprintf(os.Stderr, "docrefs: walking %s: %v\n", abs, err)
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
	// Checked rather than deferred-and-dropped: a failing write leaves bufio holding the
	// error, and discarding it exits 0 with truncated output — a fail-open, in a program
	// whose consumer reads a count and a floor.
	defer func() {
		if err := out.Flush(); err != nil {
			fmt.Fprintf(os.Stderr, "docrefs: writing output: %v\n", err)
			os.Exit(1)
		}
	}()

	for _, p := range files {
		// A directory or a broken symlink can carry a `.md` name, and reading either fails.
		// Skipping keeps the diagnosis rather than the catch: check-docs.sh reads this
		// program's exit status, so dying would fail loudly either way, but only skipping
		// here makes the run name the broken link target instead of a dead extractor.
		// Pinned by the selftest's directory case, which asserts the extractor-failure
		// message is *absent*.
		body, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		text := string(body)
		rel, err := filepath.Rel(abs, p)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		if strings.HasSuffix(rel, ".md") {
			for _, target := range extractLinks(text) {
				fmt.Fprintf(out, "link\t%s\t%s\n", rel, target)
			}
		}
		if !strings.Contains(text, "§") {
			continue
		}
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
				fmt.Fprintf(out, "cite\t%s\t%s\texternal\t%s\n", rel, target, shown)
				continue
			}
			verdict := "absent"
			if firstWordMatches(namesFor(resolved), cited) {
				verdict = "resolves"
			}
			fmt.Fprintf(out, "cite\t%s\t%s\t%s\t%s\n", rel, target, verdict, shown)
		}
	}
}
