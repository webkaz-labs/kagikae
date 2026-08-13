package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The predicates are what the shell selftest can only reach end-to-end, through a
// fixture copy of the whole tree. Pinning them here is the reason this program is Go:
// each case below is a shape one of the two walks has to get right, and three of them are
// defects a review found by mutating the first implementation rather than reading it.
//
// The link cases' expectations are not read off this implementation. They were measured
// against the Python this half replaced, by running its two patterns over each shape
// below, so they pin the behaviour the port had to preserve rather than the behaviour it
// happens to have.

func linkTargets(markdown string) string {
	return strings.Join(extractLinks(markdown), ",")
}

func TestExtractLinksSkipsSpansAndFencesAndKeepsTheRest(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"a bare link", "[x](y)\n", "y"},
		{"a single-backtick span", "`[x](y)`\n", ""},
		// The idiom AGENTS.md uses, and the shape a single-backtick rule turned into a
		// gate failure on correct prose.
		{"a double-backtick span", "``[x](y)``\n", ""},
		// Prefixed, because at column 0 fenceLineRe consumes the line as a fence toggle and
		// stripCodeSpans never runs on it: with stripCodeSpans replaced by the identity, the
		// unprefixed form passed while other rows here failed, so it was pinning the fence
		// toggle and not the span rule.
		{"a triple-backtick span", "x```[x](y)```\n", ""},
		// The one shape where this and the Python it replaced DISAGREE, so it is pinned
		// rather than described. The Python backtracks the unmatched run of three down to a
		// pair, leaving one backtick live, which then pairs with the one before `kae` and
		// swallows the link; nothing here stays live, so the link survives. Both outputs
		// measured against both implementations.
		{"an unmatched run before a link and a span", "the ``` marker and [x](y) and `kae pin`\n", "y"},
		// An over-long close still closes: the extra backticks are free to open the next
		// span, and there is no next span here.
		{"a close longer than its open", "`[x](y)``\n", ""},
		// An open longer than its close does not close, so the link is visible again —
		// the direction every gap in this half fails toward.
		{"a close shorter than its open", "``[x](y)`\n", "y"},
		{"spans either side of a link", "`a` [x](y) `b`\n", "y"},
		{"an unclosed span", "`unclosed [x](y)\n", "y"},
		// The unclosed run is emitted literally, so it stays inside the target linkRe
		// captures. Without that branch the target comes out as `y` — measured, and byte
		// identical over this tree either way, so nothing but this row pins it.
		{"an unclosed run is kept literally", "[x](`y)\n", "`y"},
		{"a span inside a link target", "[x](`y`)\n", ""},
		{"a backtick inside a double span", "``a`b`` [x](y)\n", "y"},
		{"a span whose content is a backtick", "`` ` `` [x](y)\n", "y"},
		{"a real link before a span", "[a](b) `[c](d)`\n", "b"},
		{"two links on one line", "[a](b) and [c](d)\n", "b,d"},
		// A fence is state, and the state is the only thing that crosses a line.
		{"a backtick fence", "[a](b)\n```\n[c](d)\n```\n[e](f)\n", "b,f"},
		{"a tilde fence", "[a](b)\n~~~\n[c](d)\n~~~\n[e](f)\n", "b,f"},
		{"an indented fence", "  ```bash\n  [c](d)\n  ```\n[e](f)\n", "f"},
		{"an unclosed fence swallows the rest", "[a](b)\n```\n[c](d)\n", "b"},
		// A ceiling, pinned so it is not mistaken for a defect: at column 0 an inline span
		// of three backticks is read as a fence marker, and the state latches, so every
		// link up to the next latch line is skipped. CommonMark says the opposite — a fence's
		// info string may not contain a backtick, so this is a span — which puts it among
		// the gaps that fail toward a MISSED link rather than a false broken one.
		// Inherited from the Python, whose fence pattern is the same.
		{"a column-0 inline span latches the fence", "```[x](y)```\n[real](z)\n", ""},
		// Joining first would form a `](` pair nobody wrote. This is why the link half
		// runs per line while the citation half joins.
		{"a pair formed across a line break", "see [a]\n(b) here\n", ""},
		{"a fragment is kept for the caller to strip", "[a](b.md#frag)\n", "b.md#frag"},
	} {
		if got := linkTargets(tc.in); got != tc.want {
			t.Errorf("%s: extractLinks(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// No case here pins the `.md` restriction on the link half, because that decision lives
// in main() and reaching it means running the program. It is pinned anyway, and loudly:
// the walk reads `.go` for citations, so the link-shaped fixtures in the table above
// would become link rows with targets that resolve nowhere, and check-docs.sh's baseline
// selftest case would fail on this file. Widening the link half to Go is therefore not a
// silent change — which is the property the citation fixtures buy with their ZZ names.

func TestSectionNamesTakeHeadingsListTitlesAndAnchoredLabels(t *testing.T) {
	doc := "## Tool Tiers\n" +
		"- **Every credential copy** kae keeps can be killed\n" +
		"**Mechanisms.** Internally: global shared\n" +
		"prose that mentions **both** halves mid-sentence\n"
	got := sectionNames(doc)
	first := map[string]bool{}
	for _, n := range got {
		first[n[0]] = true
	}
	// `mechanisms`, not `mechanisms.`: a trailing period is decoration on both sides, so
	// `**Mechanisms.**` and a citation writing `§ Mechanisms` have to meet.
	for _, want := range []string{"tool", "every", "mechanisms"} {
		if !first[want] {
			t.Errorf("expected a section name beginning %q, got %v", want, got)
		}
	}
	// Mid-sentence emphasis is not a section name. Accepting it made `§ Both open
	// gates` resolve against the word *both* in a sentence: on docs/ROADMAP.md the
	// unanchored form admits roughly three times the first-words the anchored one does.
	if first["both"] {
		t.Errorf("mid-sentence emphasis must not be a section name: %v", got)
	}
}

func TestSectionNamesIgnoreHeadingShapedLinesInsideFences(t *testing.T) {
	doc := "## Real Heading\n\n```bash\n# canonical smoke ordering\n```\n"
	got := sectionNames(doc)
	for _, n := range got {
		if n[0] == "canonical" {
			t.Fatalf("a shell comment inside a fence became a section name: %v", got)
		}
	}
	// Heading-shaped lines live inside fenced blocks all over this tree — stripFences says
	// how many were measured — so the negative above is the whole point; this keeps it from
	// passing because nothing was read.
	if len(got) != 1 || got[0][0] != "real" {
		t.Fatalf("expected the one real heading, got %v", got)
	}
}

func TestSectionNamesJoinALabelThatWraps(t *testing.T) {
	doc := "- **A relogin's pre-flight refusal owes a backup it cannot\n" +
		"  safely take yet** (recorded 2026-08-08)\n"
	got := sectionNames(doc)
	if !firstWordMatches(got, words("A relogin's pre-flight refusal owes")) {
		t.Fatalf("a wrapped list title must still be a section name: %v", got)
	}
}

func TestFirstWordMatchesAcceptsAShortenedCitationAndRejectsAPhantom(t *testing.T) {
	names := sectionNames("## kae pin and mise init Semantics\n## Tier-2 tools\n")
	// The idiom shortens the name, which is why the predicate is a prefix test.
	if !firstWordMatches(names, words("kae pin, `removeDirCredential` for the rule")) {
		t.Error("a citation naming a leading fragment must resolve")
	}
	// The citation this repository actually shipped.
	if firstWordMatches(names, words("Tier-1 tools for the mapping")) {
		t.Error("`Tier-1 tools` must not resolve against `Tier-2 tools`")
	}
	if firstWordMatches(names, nil) {
		t.Error("an empty citation must not resolve")
	}
}

func TestWordsDropsDecorationAndTrailingPunctuationOnly(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"`kae pin`,", "kae pin"},
		{"**Tier-2 tools**", "tier-2 tools"},
		{"A relogin's pre-flight.", "a relogin s pre-flight"},
		{"Locking;", "locking"},
	} {
		got := words(tc.in)
		joined := ""
		for i, w := range got {
			if i > 0 {
				joined += " "
			}
			joined += w
		}
		if joined != tc.want {
			t.Errorf("words(%q) = %q, want %q", tc.in, joined, tc.want)
		}
	}
	// A hyphen has to survive: `Tier-1` versus `Tier-2` is the entire difference the
	// shipped defect turned on.
	if words("Tier-1")[0] == words("Tier-2")[0] {
		t.Error("a hyphenated name must not normalise to its stem")
	}
}

// The regex half: a phantom written next to a valid citation was passing because the
// name window was a consuming capture, so `FindAll` resumed past the second citation.
// Five live citations went unemitted. This asserts both are seen.
// The fixture names a target that exists nowhere in this repository, deliberately. This
// program reads `.go` files, so a citation-shaped string in its own test is a citation
// as far as the walk is concerned — the first version of this test named docs/ROADMAP.md
// and put two `absent` rows into the live run, which is the shell selftest's fixture
// problem recurring one file over. A target that resolves nowhere lands in `external`,
// which check-docs.sh skips.
func TestBothCitationsOnOneLineAreEmitted(t *testing.T) {
	line := "See [A.md](docs/ZZFIXTURE-A.md) § kae add and [B.md](docs/ZZFIXTURE-B.md) § Tier-1 tools here.\n"
	joined := unwrap(stripFences(line))
	locs := citeRe.FindAllStringSubmatchIndex(joined, -1)
	if len(locs) != 2 {
		t.Fatalf("expected both citations on one line, got %d", len(locs))
	}
	// And the second one is the phantom, read from the match end rather than from the
	// end of the filename group.
	tail := joined[locs[1][1]-1:]
	if got := words(tail)[0]; got != "tier-1" {
		t.Fatalf("second citation read as %q, want tier-1", got)
	}
	if firstWordMatches(sectionNames("## Tier-2 tools\n"), words(tail)) {
		t.Fatal("the phantom must not resolve")
	}
}

// unwrap's comment-marker strip had no test, and a review found that removing it drops
// eight citations from the walk while check-docs.sh, its selftest and `go test` all
// report clean — the floor cannot see 186 any more than it can see 139.
func TestUnwrapJoinsACitationWrappedAcrossTwoCommentLines(t *testing.T) {
	src := "// the rule and the reproduction live on docs/ROADMAP.md\n" +
		"// § Rotation is measured for claude only, which gates it\n"
	joined := unwrap(src)
	locs := citeRe.FindAllStringSubmatchIndex(joined, -1)
	if len(locs) != 1 {
		t.Fatalf("a citation wrapped across two comment lines must be found once, got %d in %q", len(locs), joined)
	}
	if got := words(joined[locs[0][1]-1:])[0]; got != "rotation" {
		t.Fatalf("read the cited name as %q, want rotation", got)
	}
	// Without the strip the continuation keeps its `//`, so the sigil is preceded by a
	// comment marker instead of the filename and nothing matches.
	if len(citeRe.FindAllStringSubmatchIndex(strings.ReplaceAll(src, "\n", " "), -1)) != 0 {
		t.Fatal("this asserts the strip is what joins them, not the newline removal")
	}
}

func TestResolveTargetTakesTheCitingDirectoryAndTheRootAndNothingElse(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel string) {
		if err := os.WriteFile(filepath.Join(root, rel), []byte("## Not converged\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("docs/CONTEXT.md")
	write("ROOT.md")

	// Relative to the citing file, which is how docs/ cites its siblings.
	if resolveTarget(root, "docs/ADAPTERS.md", "CONTEXT.md") == "" {
		t.Error("a sibling under docs/ must resolve relative to the citing file")
	}
	// Repo-root, which is how a Go comment writes docs/CONTEXT.md.
	if resolveTarget(root, "internal/cmd/x.go", "docs/CONTEXT.md") == "" {
		t.Error("a repo-root path must resolve from anywhere")
	}
	// And nothing else. A basename search under docs/ used to be a third candidate; it
	// reported a wrong directory as `resolves` and counted it, so the gate called a typo
	// checked and correct.
	if got := resolveTarget(root, "README.md", "doc/CONTEXT.md"); got != "" {
		t.Errorf("a wrong directory must not resolve by basename, got %q", got)
	}
	if got := resolveTarget(root, "README.md", "NO-SUCH-FILE.md"); got != "" {
		t.Errorf("a target that exists nowhere must not resolve, got %q", got)
	}
}
