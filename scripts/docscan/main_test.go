package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJaccardScoresIdenticalAndDisjointText(t *testing.T) {
	long := strings.Fields("a bind writes the account recorded identity into that directory store " +
		"and attribution then compares the account recorded identity against exactly that")
	other := strings.Fields("the runner refuses a block whose checks are comments because a comment " +
		"asserts nothing and the block still reports every line exited zero")

	same := jaccard(shingles(long, shingleN), shingles(long, shingleN))
	if same != 1 {
		t.Fatalf("identical word streams must score 1, got %v", same)
	}
	if got := jaccard(shingles(long, shingleN), shingles(other, shingleN)); got != 0 {
		t.Fatalf("texts sharing no 6-gram must score 0, got %v", got)
	}

	// A real partial overlap, so the metric is exercised between its two ends
	// rather than only at them.
	half := append(append([]string{}, long[:12]...), other[:12]...)
	mid := jaccard(shingles(long, shingleN), shingles(half, shingleN))
	if mid <= 0 || mid >= 1 {
		t.Fatalf("a partial overlap must score strictly between 0 and 1, got %v", mid)
	}

	// Shorter than one shingle: an empty set, and no division by zero.
	if got := jaccard(shingles(long[:3], shingleN), shingles(long, shingleN)); got != 0 {
		t.Fatalf("a stream shorter than one shingle must score 0, got %v", got)
	}
}

// shingles is the metric's real input, and asserting a set against itself — which
// every other test here does — cannot see a consistent bug in it. A review proved
// that by dropping the last window and watching a real score change from 0.61 to
// 0.59 with the suite still green. So pin the windows exactly.
func TestShinglesAreTheExactWindows(t *testing.T) {
	words := []string{"a", "b", "c", "d", "e", "f", "g"}
	got := shingles(words, shingleN)
	want := []string{"a b c d e f", "b c d e f g"}
	if len(got) != len(want) {
		t.Fatalf("want %d windows over %d words, got %d: %v", len(want), len(words), len(got), got)
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing window %q", w)
		}
	}
	if len(shingles(words[:shingleN-1], shingleN)) != 0 {
		t.Error("fewer words than one window must yield no window")
	}
	if len(shingles(words[:shingleN], shingleN)) != 1 {
		t.Error("exactly one window's worth of words must yield exactly one window")
	}

	// The tokenizer keeps digits and underscores, because identifiers carry them.
	if strings.Join(normalize("Codex_Auth cli|2 dirs"), " ") != "codex_auth cli 2 dirs" {
		t.Errorf("normalize dropped a digit or an underscore: %q", normalize("Codex_Auth cli|2 dirs"))
	}
}

// The report prints a prefix of the pairs and says how many it did not print, so
// both claims rest on the sort. A review reversed it and watched the header promise
// five pairs while the body printed none, with every test still passing.
func TestPairsAreSortedByScoreDescendingWithAStableTiebreak(t *testing.T) {
	long := normalize("the bind writes the account recorded identity into that directory store " +
		"and attribution compares it against exactly that copy before deciding anything")
	half := append(append([]string{}, long[:14]...), normalize("entirely unrelated words about shells and completion scripts")...)
	paras := []paragraph{
		prepare(paragraph{file: "a.md", line: 1, words: long}),
		prepare(paragraph{file: "b.md", line: 1, words: half}),
		prepare(paragraph{file: "c.md", line: 1, words: long}),
	}
	// Every paragraph shares the anchor, so all three pairs exist: a-c is identical
	// prose and scores 1, the two involving b score less.
	got := comparePairs(paras, indexAnchors(paras, map[string]bool{"attribution": true, "shells": true}))
	if len(got) != 3 {
		t.Fatalf("want 3 pairs, got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].score < got[i].score {
			t.Fatalf("pairs are not sorted descending: %v", got)
		}
	}
	if got[0].a != 0 || got[0].b != 2 {
		t.Errorf("the identical pair must sort first, got (%d,%d)", got[0].a, got[0].b)
	}
	// The comparator itself, because a sort of three elements cannot pin it: with
	// two equal scores an unstable sort leaves them where they are, so removing the
	// index tiebreak passed the ordering assertion above.
	hi := pair{a: 5, b: 9, score: 0.9}
	lo := pair{a: 0, b: 1, score: 0.1}
	if !pairLess(hi, lo) || pairLess(lo, hi) {
		t.Error("pairLess must order the higher score first, both ways round")
	}
	first := pair{a: 0, b: 2, score: 0.5}
	second := pair{a: 0, b: 3, score: 0.5}
	third := pair{a: 1, b: 2, score: 0.5}
	if !pairLess(first, second) || pairLess(second, first) {
		t.Error("equal scores must break on the second index, both ways round")
	}
	if !pairLess(first, third) || pairLess(third, first) {
		t.Error("equal scores must break on the first index, both ways round")
	}
	if pairLess(first, first) {
		t.Error("pairLess must be irreflexive, or sort.Slice has no total order to work with")
	}

	// countAtLeast reads that order, and the report slices on it.
	if n := countAtLeast(got, 1.0); n != 1 {
		t.Errorf("countAtLeast at 1.0 must be 1, got %d", n)
	}
	if n := countAtLeast(got, 0); n != 3 {
		t.Errorf("countAtLeast at 0 must be all of them, got %d", n)
	}
	if n := countAtLeast(got, 1.01); n != 0 {
		t.Errorf("countAtLeast above every score must be 0, got %d", n)
	}
}

func TestComparableIsTheWordFloor(t *testing.T) {
	at := make([]string, minWords)
	for i := range at {
		at[i] = "word"
	}
	if !comparable(paragraph{words: at}) {
		t.Errorf("a unit of exactly %d words must be comparable", minWords)
	}
	if comparable(paragraph{words: at[:minWords-1]}) {
		t.Errorf("a unit of %d words must not be", minWords-1)
	}
}

func TestExcludedDirSkipsGitAndTheGeneratedExport(t *testing.T) {
	for _, path := range []string{".git", "dist", ".claude/skills/go-cli-tooling", ".claude/skills/go-cli-tooling/references"} {
		if !excludedDir(path) {
			t.Errorf("%q must be excluded from the walk", path)
		}
	}
	// The other half, and the reason `.claude` is walked rather than skipped whole:
	// the upstream-auth-drift skill under it is cited as normative.
	for _, path := range []string{".", "docs", ".claude", ".claude/skills", ".claude/skills/upstream-auth-drift", "internal/cmd"} {
		if excludedDir(path) {
			t.Errorf("%q must be walked", path)
		}
	}
}

func TestDeclaredNamesReachesFieldsAndTypesNotOnlyFuncs(t *testing.T) {
	const src = `package p

type harvestRefusal struct {
	Ordered      bool
	Unattributed bool
	no           int
}

type attributionSource int

const credStoreSegment = "credstore"

func (app *App) credStoreReaders() {}

func short() {}
`
	f, err := parser.ParseFile(token.NewFileSet(), "p.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := map[string]bool{}
	for _, n := range declaredNames(f) {
		got[n] = true
	}
	// The struct fields are the point: an anchor set of funcs and constants alone
	// misses the vocabulary this repository decides with.
	for _, want := range []string{
		"harvestRefusal", "Ordered", "Unattributed", "attributionSource",
		"credStoreSegment", "credStoreReaders",
	} {
		if !got[want] {
			t.Errorf("declaredNames missed %q", want)
		}
	}
	// The length floor: `no` is a genuine struct field, so this fails on a
	// declaredNames without the floor. A review caught the first version of this
	// assertion pairing it with `p`, the package name — which no node kind in the
	// switch above ever reaches, so it demonstrated nothing about the floor.
	if got["no"] {
		t.Errorf("declaredNames returned %q, which is under the length floor", "no")
	}
}

func TestParagraphsSkipsFencedCodeAndSplitsTableRows(t *testing.T) {
	const md = "# Heading\n" +
		"\n" +
		"first paragraph line one\n" +
		"first paragraph line two\n" +
		"\n" +
		"```bash\n" +
		"kae pin main\n" +
		"```\n" +
		"\n" +
		"| term | names |\n" +
		"| reader | a config dir reading a store |\n"

	got := paragraphs("docs/X.md", md)
	if len(got) != 3 {
		t.Fatalf("want 3 units (one paragraph, two table rows), got %d: %+v", len(got), got)
	}
	if got[0].line != 3 || strings.Join(got[0].words, " ") != "first paragraph line one first paragraph line two" {
		t.Errorf("paragraph unit wrong: line %d words %q", got[0].line, got[0].words)
	}
	for _, p := range got {
		for _, w := range p.words {
			if w == "kae" || w == "pin" {
				t.Fatalf("fenced code leaked into a unit: %q", p.words)
			}
		}
	}
	if got[1].line != 10 || got[2].line != 11 {
		t.Errorf("table rows must carry their own line numbers, got %d and %d", got[1].line, got[2].line)
	}
}

// The fixture is the real glossary's shape, both halves of which a review found the
// first version getting wrong: it harvested the five body rows of the routing table
// (whose first column is a question, not a term) and it silently skipped the two
// rows whose first cell carries a parenthetical or a second term.
func TestContextTermsReadsTheTermTablesAndOnlyThose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CONTEXT.md")
	body := "# kagikae vocabulary\n" +
		"\n" +
		"| question | authority |\n" +
		"|----------|-----------|\n" +
		"| what a decision does | the predicate named in the entry |\n" +
		"\n" +
		"## Surface terms\n" +
		"\n" +
		"| term | names |\n" +
		"|------|-------|\n" +
		"| `account` | a login snapshot |\n" +
		"\n" +
		"## Mechanism terms\n" +
		"\n" +
		"| **bound directory** | a directory kae pin has bound | |\n" +
		"| **mode** (`shared`, `isolated`) | which mechanism a binding uses | |\n" +
		"| **supersedes**, **orderable** | the two ordering predicates | |\n" +
		"\n" +
		"## Not converged\n" +
		"\n" +
		"| pinned directory | prose that has not converged | |\n" +
		"not a table row at all\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := contextTerms(path)
	if err != nil {
		t.Fatalf("contextTerms: %v", err)
	}
	want := map[string]bool{
		"account": true, "bound directory": true,
		"mode": true, "supersedes": true, "orderable": true,
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("contextTerms returned %q — a table header, a routing question, or outside the term sections", g)
		}
		delete(want, g)
	}
	if len(want) != 0 {
		t.Errorf("contextTerms missed %v", want)
	}
}

// The anchoring is the whole design: identical prose about two different
// mechanisms is not a duplication finding, and prose about one mechanism is worth
// reading even when the wording diverges. Both directions are asserted, because a
// comparePairs that ignored the buckets would satisfy only the first.
func TestPairsNeedASharedAnchor(t *testing.T) {
	const shared = "the bind writes the account recorded identity into that directory store and " +
		"attribution compares the account recorded identity against exactly that copy today"

	withAnchor := []paragraph{
		prepare(paragraph{file: "a.md", line: 1, words: normalize(shared + " credStoreReaders")}),
		prepare(paragraph{file: "b.md", line: 1, words: normalize(shared + " credStoreReaders")}),
	}
	anchors := map[string]bool{"credStoreReaders": true}
	buckets := indexAnchors(withAnchor, anchors)
	pairs := comparePairs(withAnchor, buckets)
	if len(pairs) != 1 {
		t.Fatalf("two paragraphs sharing an anchor must produce one pair, got %d", len(pairs))
	}
	if pairs[0].score <= 0.9 {
		t.Errorf("near-identical prose must score high, got %v", pairs[0].score)
	}

	// Same prose, no anchor in the set: no pair at all.
	noAnchor := []paragraph{
		prepare(paragraph{file: "a.md", line: 1, words: normalize(shared)}),
		prepare(paragraph{file: "b.md", line: 1, words: normalize(shared)}),
	}
	buckets = indexAnchors(noAnchor, anchors)
	if got := comparePairs(noAnchor, buckets); len(got) != 0 {
		t.Fatalf("paragraphs sharing no anchor must produce no pair, got %d", len(got))
	}
}

// A regression test for a defect this tool shipped with for one afternoon: an
// earlier indexAnchors skipped any anchor present in over 5% of paragraphs, and
// the highest-scoring pair in the repository — an exact 1.00 match between two
// docs/RELEASE.md entries — was invisible because the anchors it shared were
// `account` and the tool names. A ubiquitous anchor is still an anchor.
func TestAUbiquitousAnchorStillBuckets(t *testing.T) {
	words := normalize("a paragraph long enough to carry a shingle set and the word account in it")
	paras := []paragraph{
		prepare(paragraph{file: "a.md", line: 1, words: words}),
		prepare(paragraph{file: "b.md", line: 1, words: words}),
		prepare(paragraph{file: "c.md", line: 1, words: words}),
	}
	buckets := indexAnchors(paras, map[string]bool{"account": true})
	if len(buckets["account"]) != 3 {
		t.Fatalf("an anchor in every paragraph must bucket all of them, got %v", buckets)
	}
	if got := comparePairs(paras, buckets); len(got) != 3 {
		t.Fatalf("three identical paragraphs must produce three pairs, got %d", len(got))
	}
}

// One matching rule for every anchor, and both halves of it are load-bearing.
// The plural: the glossary names "reader" and the prose says "readers" more often.
// The words-without-adjacency: two reviews between them showed that matching a
// multi-word term as an adjacent substring cannot reach "bound directories" and
// contributed no pair at all, which is the half of the anchor set that justifies
// reading docs/CONTEXT.md in the first place.
func TestAnchorsMatchByWordsAndPlurals(t *testing.T) {
	paras := []paragraph{
		prepare(paragraph{file: "a.md", line: 1, words: normalize(
			"the readers that named another account disagree about whose login this copy is",
		)}),
		prepare(paragraph{file: "b.md", line: 1, words: normalize(
			"every directory that kae has bound keeps its own sessions beside the shared credential",
		)}),
	}
	for anchor, want := range map[string]int{
		"reader":          1, // singular anchor, plural in the prose
		"bound directory": 1, // phrase whose words are four apart and in the other order
		"harvest":         0, // control: absent
		"bound harvest":   0, // control: one word present, one absent
	} {
		if got := len(indexAnchors(paras, map[string]bool{anchor: true})[anchor]); got != want {
			t.Errorf("anchor %q matched %d paragraphs, want %d", anchor, got, want)
		}
	}
}
