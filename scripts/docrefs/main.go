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
// mise.toml declares no python, so a python3 the gate needed was a dependency nothing
// declared and nothing outside a developer's machine would notice. The port is also what
// made docs-check cheap enough for CI — bash plus one `go run`, no interpreter for a
// runner to install. Its selftest stayed out; .github/workflows/check.yml's header owns
// both halves of that decision. To re-check that the gate needs no interpreter, put a
// python3 that exits 127 first on PATH and run `mise run check`, which must exit 0; do
// it that way rather than by grepping for `python3`, which answers a different question
// and finds this paragraph. This program is also inside `go vet`, `golangci-lint` and
// `go test`, which is where main_test.go's cases live, and the release is unaffected
// because .goreleaser.yaml builds `main: .`.
//
// # The link predicate, and what it cannot see
//
// A link-shaped string inside code is an example, not a link, so fenced blocks and inline
// code spans are removed first. AGENTS.md's citation rule contains a bracketed `X.md`
// pair as an illustration of a grep form, and the first real run of this check reported it
// as a broken target.
//
// The ceilings, with no instance in this tree today. A tilde fence is tracked as well as a
// backtick one, but a four-space indented code block is not, so a link inside one is
// reported as broken. Reference-style links (`[x][1]` with a separate `[1]: path`
// definition), links split across lines, and `[a [b]](x)` are invisible to linkRe. And a
// line can end where this walk does not think it ends: the Python read a document through
// universal newlines and then splitlines(), so a lone CR — and VT, FF, FS, GS, RS, NEL, LS
// and PS — ended a line there where none of them does here, and a fence marker after one of
// those toggles in one implementation and not the other, inverting the fence state for the
// rest of the file.
//
// Which way each one fails is the part worth knowing, and it is stated as a rule rather than
// as a list, because every attempt at listing it was one condition short: the indented code
// block is the only loud one — a link inside it is reported as broken — and every other gap
// above is silent, because a link nothing extracts is a link nothing checks. The separator
// class manages both at once, measured doing so in a single file: a link inside a fenced
// example emitted as a broken target while a real link after it is dropped.
//
// One of the silent ones is worth naming, because a reader will otherwise take it for a
// defect: a column-0 run of backticks that CommonMark reads as an inline span, which
// fenceLineRe reads as a fence marker instead, so the state latches and every link up to the
// next latch line is skipped. Inherited, the Python's fence pattern being the same one, and
// main_test.go pins it.
//
// CommonMark's rule for that one — a *backtick* fence's info string may not hold a backtick,
// which is why a tilde marker carrying one is a real fence and not a latch — is also the
// predicate that decides whether this tree has an instance, and it is one grep for a fence
// marker with a further backtick after it on the same line. A count of fence-marker lines
// does NOT decide it: a pair of latch lines leaves that count even, so parity sees a net
// inversion at EOF and says nothing about the links between
// them. The other ceilings above have no such predicate, and the greps a reader reaches for
// return false positives on all of them — an indented list continuation reads as an indented
// code block, and a bracketed pair inside a regex reads as a reference-style link — so those
// are "no instance" by reading the hits, not by a command.
//
// Two more ceilings, these with instances today. A fence inside a blockquote matches neither
// fence model — README.md has one — so a link inside such a block is walked as if it were
// prose, here and in the Python alike. And a link target containing a tab splits into two
// fields in check-docs.sh's read loop, so the diagnostic names only the part before it; the
// gate still fails, but a *trailing* tab is eaten as field whitespace and the target
// resolves, which was equally true of the two-variable loop this replaced.
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
//     docs/SCOPE-MODEL.md) and `§A` (docs/VALIDATION.md naming a lettered step
//     marker inside its own fenced block), which introduce no searchable name. The
//     lettered form used to be mostly docs/RELEASE.md's per-release sub-sections,
//     product code included; those are gone and the exclusion outlived them.
//     Note which half does the work: no live instance is
//     written with a space there, so widening the character class to admit digits would
//     change nothing. That is a property of the tree rather than of this program, and
//     TestSectionNumbersAreWrittenWithNoSpaceAfterTheSigil is where it is checked — it
//     used to be a `git grep` quoted here, which went false when one was added to a
//     document in a commit that never opened this file.
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
//     (`§ kae pin` for a heading reading `## Semantics of kae pin`, illustrated rather
//     than quoted for the same reason ZZEXAMPLE is above). The interior form is why
//     widening is not just a
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
//     derive them rather than reading one here.
//   - A target that resolves nowhere is `external`, which check-docs.sh skips without
//     counting, so it can never fail. A citation naming a file that does not exist is
//     therefore silent here; the link walk catches it only when it is written as a
//     markdown link, and most citations in Go comments are not.
package main

import (
	"bufio"
	"errors"
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
	// Leading whitespace as well as column 0, which is what the selftest's fenced-citation
	// case guarantees: the column-0 form fails that case and nothing else. Named rather than
	// numbered, because the numbers move. Three copies of this model had
	// three dialects; merging the two here leaves two copies of the model and one dialect,
	// and scripts/docscan/main.go still carries the third (no `~~~`). That was the argument
	// for matching a sibling rather than for the strictness.
	fenceRe = regexp.MustCompile("(?ms)^" + fenceMarker + ".*?^" + fenceMarker + "[^\n]*\n?")
	// The link half's fence, built from the same fenceMarker as the block form above so the
	// dialect moves as one rather than by hand in two literals, and deliberately a different
	// model: see the package comment.
	fenceLineRe = regexp.MustCompile("^" + fenceMarker)
	// Not line-anchored, unlike the fence above: an HTML comment opens and closes
	// mid-line as readily as on its own, and the lazy `.*?` under `(?s)` stops at the
	// first `-->` so two comments do not merge into one span.
	htmlCommentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
	linkRe        = regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)
	commentRe     = regexp.MustCompile(`^\s*(?://+|#+)\s*`)
	decorationRe  = regexp.MustCompile("[`*_\"'()\\[\\]|~]")
	trailingRe    = regexp.MustCompile(`[.,;:]+$`)
)

// nameWindow bounds how much text after the sigil is read as the cited name. Only its
// first word decides the verdict; the rest is carried so a diagnostic can quote enough
// of the citation to find it.
const nameWindow = 90

// fenceMarker is the dialect both fence models share: leading whitespace, then either
// marker. Named rather than spelled twice, because the two regexes claimed to share a
// dialect while each carrying its own copy of it — which is how three copies of this model
// came to have three dialects in the first place.
const fenceMarker = "[ \t]*(?:```|~~~)"

// out is package level so readDoc can flush it, which is the whole reason it is not a local
// in main, and initialised here rather than in main so readDoc has no nil case to test — a
// branch that was measured unreachable in the built program.
var out = bufio.NewWriter(os.Stdout)

// readDoc reads a document the walk has already accepted as one, and a failure is fatal.
//
// One function for both readers, because two policies at two sites produced a false claim.
// The walk's read exited; namesFor's cached nil silently, so a citation whose target could
// not be opened was judged against zero section names and reported as naming no section that
// file declares — a claim about a file nothing had read, printed beside the true message,
// with sort order deciding which came first. Measured on a mode-000 target before this was
// one function.
//
// Flushed before exiting because by here bufio has already written whole buffers, so exiting
// without it truncates stdout mid-row, the caller's heredoc completes that row, and its loop
// reads a bogus verdict out of the fragment — a diagnosis pointing at the arm that catches a
// verdict typo, for a typo that does not exist. Measured over one fixture per tracked
// document: the fragment produced that message at several positions before the flush and at
// none after. It is not free — flushing emits more rows whose target could not be read, so
// the phantom-citation complaints grow with them — and it is still the better side, because
// the true message comes first either way. No count is written for either half: which
// position the boundary lands on depends on how much output precedes the failing file, which
// is also why no selftest case pins the flush and one attempt at pinning it was measured
// vacuous. It is defence in depth and claims nothing more.
func readDoc(p string) []byte {
	body, err := os.ReadFile(p)
	if err != nil {
		_ = out.Flush()
		fmt.Fprintf(os.Stderr, "docrefs: reading %s: %v\n", p, err)
		os.Exit(1)
	}
	return body
}

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
// captured backtick run required again to close does not compile here.
//
// The two are NOT equivalent, and the difference is in the outcome and not only in the
// leftover bytes. The rule: on a run
// of N backticks with no closing run of at least N, the Python backtracks and consumes
// 2*floor(N/2) of them as an empty span, so N mod 2 backticks stay live and re-phase every
// later pairing on the line; this emits the whole run literally and leaves nothing live. The
// shape is main_test.go's unmatched-run case, which is where it belongs, a fixture being
// executable where a comment is not — and where a backtick *pair* cannot go: gofmt rewrites
// one in a doc comment to a curly quote as godoc's old quoting idiom, and `mise run check`
// fails on the rewritten file, so writing a pair here is a gate failure with no explanation
// in the file. Measured: a triple run and single backticks survive gofmt byte-identically, so
// the hazard is the pair and nothing else. The two strips already differ on this tree — the way to
// see where is to run both over every line that reaches them, and the differing lines include
// the bracketed illustration AGENTS.md's citation rule carries and docs/ROADMAP.md's
// quotation of it — so byte-identical *output* today is an accident of where those sentences
// wrap rather than a property.
//
// Kept rather than reverted to the Python's shape, and not on the direction argument alone,
// which covers half the class: for an odd unmatched run this exposes a link the Python hid,
// which fails the gate loudly on prose where hiding one leaves a broken target silent, but
// for an even one the residue lands inside link syntax and it can run either way. What
// settles both is that an unclosed run is literal text in CommonMark, so this side is the
// correct reading. The evidence for the port is the narrow kind and not an equivalence: the
// link rows were byte-identical over this tree at the port, re-derivable with
// `git show 72e24f5:scripts/docs_links.py`, and every shape in main_test.go was measured
// against the Python before it was written down.
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
	for _, line := range strings.Split(stripHTMLComments(markdown), "\n") {
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
//
// stripHTMLComments is separate from stripFences for one caller: extractLinks does not use
// the block fence model at all, toggling per line instead, so it cannot reach a strip folded
// into stripFences — and the strip has to happen before any line split, since a comment
// opens and closes across lines. Two callers, one seam; the alternative shipped in this
// file's first version and left the link half seeing comments the other two did not.
func stripHTMLComments(text string) string {
	return htmlCommentRe.ReplaceAllString(text, "")
}

// stripHTMLComments runs first, and every consumer of this walk calls one of the two.
// CommonMark renders `<!-- … -->` as raw HTML, so a heading inside a comment declares no
// section a reader can reach, a citation inside one is not a citation, and a link inside
// one is not a link. The defect that earned it: a section cited as normative from outside
// its file was wrapped in `<!--`/`-->` on a clone of 75a67d2 and the gate reported ok, with
// TestTheCitedSkillSectionHasNoFirstWordRival green beside it, because a `#` line matches
// headingRe regardless of what encloses it. Commenting a section out while reworking it is
// an ordinary edit. It changes nothing this program emits on this tree — remove the call
// and the output is byte-identical, which is the control to re-run rather than a grep for
// the literal, since this comment and main_test.go's fixture both contain one.
//
// Three ceilings, no instance in this tree, each measured on a fixture:
//
//   - The order. An unclosed `<!--` inside a fenced example swallows text up to the next
//     `-->`, the closing fence marker included; the fence-first order trades this for the
//     reverse.
//   - Fail-open on the link half, the direction that matters. A comment closing
//     immediately before a fence marker (`-->` and ```` ```go ```` sharing a line) collapses
//     to a bare marker line, which toggles extractLinks' per-line fence and hides a link
//     CommonMark does produce. Measured against the parent commit, which reported it. The
//     paying-for-it side is the opposite construction, a link whose label is interrupted
//     by a multi-line comment, which CommonMark forms and only the strip finds — the trade
//     was taken in that direction on purpose, and preserving line counts instead of
//     removing the span loses the gain without closing the loss.
//   - Comments are the only raw HTML read this way. A `##` line inside a `<pre>` or `<div>`
//     block is literal text to a renderer and reachable to nothing, and it is still
//     counted here as a heading. Closing that means parsing HTML blocks, which is not
//     bought by a construction nobody writes.
func stripFences(text string) string {
	text = stripHTMLComments(text)
	// The guard is not a micro-optimisation dressed up: `fenceRe` is `(?ms)` with a lazy
	// `.*?`, so it costs about 20ns/byte even where nothing can match, while only a small
	// minority of the sigil-bearing files hold a fence marker at all — the pair this
	// sentence used to name was off by one on both halves, so derive it instead by counting
	// the files this walk sees that contain the sigil against those that also contain a
	// marker. Measured at 41ms of this program's 216ms, paid once per invocation; the
	// package comment's note on citeRe says how many invocations one `mise run check`
	// makes. It cannot change the result: the pattern requires one of these two literals.
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
// headingNames returns the names ATX headings declare, which is the subset of
// sectionNames a reader following a citation can actually land on: a bold label declares a
// name this program resolves against and no renderer gives it an anchor. Split out for the
// second caller, TestTheCitedSkillSectionHasNoFirstWordRival, whose whole question is
// whether a surviving name is of this kind — it had its own copy of this loop, so a change
// to how a heading is read would have left the test asserting against a set the program
// does not compute. Takes text already through stripFences.
func headingNames(stripped string) [][]string {
	var names [][]string
	for _, line := range strings.Split(stripped, "\n") {
		if m := headingRe.FindStringSubmatch(line); m != nil {
			names = append(names, words(m[1]))
		}
	}
	return names
}

func sectionNames(markdown string) [][]string {
	stripped := stripFences(markdown)
	names := headingNames(stripped)
	for _, line := range strings.Split(stripped, "\n") {
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

// docFiles is the set of documents the walk hands to both extractors — not everything they
// read, since resolveTarget and namesFor stat and open a citation's target wherever it
// names, including under a pruned directory, which a selftest case depends on.
//
// It is a function rather than a loop inside main so that a test asserting a property of
// that set does not have to re-derive it. One did, briefly, and the copy folded a stat
// failure back into "not a document" — the fail-open the comment below records closing —
// so the test vouched for a tree it had silently not read while this program exited 1 on
// the same tree. One walk, one policy.
func docFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walking %s: %w", p, err)
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if hasSuffix(d.Name()) {
			// os.Stat rather than d.Type(), because it follows symlinks the way the Python's
			// is_file() did: a symlink to a document is read, while a dangling one, a symlink
			// to a directory and a FIFO are skipped before anything opens them. d.Type() on a
			// symlink is ModeSymlink, so an IsRegular test there would skip a symlink to a
			// real document instead.
			//
			// This half was dropped when the read below was made fatal, and dropping it
			// turned two non-documents into a gate nobody can act on: a dangling `x.md`
			// failed the whole run for something no docs edit can fix, and a FIFO named
			// `x.md` blocked in open(2) with no timeout anywhere in `mise run check`. The
			// FIFO hangs the same way before that change, so the fatal read exposed it
			// rather than caused it. A plain directory never reaches here at all — d.IsDir()
			// returned above — which is why the read is not where this belongs.
			//
			// A stat that fails is not automatically "not a document", and folding the two
			// together re-opened the fail-open one directory further out. ENOENT and its
			// relatives do mean not-a-document — a dangling symlink, a symlink loop — but
			// EACCES means a document this walk cannot even see. Measured with a parent
			// directory readable but not traversable: one skip for both left a whole
			// document's references unchecked at rc=0. The Python skipped it too, so this is
			// a deliberate divergence from it, in the direction where the gate is loud.
			if info, serr := os.Stat(p); serr != nil || !info.Mode().IsRegular() {
				if errors.Is(serr, fs.ErrPermission) {
					// Worded rather than handed to `%v` alone: the verb in a *fs.PathError
					// belongs to the standard library, so the selftest's needle would be a
					// string this program does not own.
					return fmt.Errorf("cannot stat %s: %w", p, serr)
				}
				return nil
			}
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
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

	files, err := docFiles(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "docrefs: %v\n", err)
		os.Exit(1)
	}

	cache := map[string][][]string{}
	namesFor := func(p string) [][]string {
		if n, ok := cache[p]; ok {
			return n
		}
		n := sectionNames(string(readDoc(p)))
		cache[p] = n
		return n
	}

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
		text := string(readDoc(p))
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
