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
	// Positive control for the length filter: without it this test would pass on a
	// version of declaredNames that returns every identifier in the file.
	for _, unwanted := range []string{"p", "no"} {
		if got[unwanted] {
			t.Errorf("declaredNames returned %q, which is under the length floor", unwanted)
		}
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

func TestContextTermsReadsBothEmphasisForms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CONTEXT.md")
	body := "| term | names |\n" +
		"|------|-------|\n" +
		"| `account` | a login snapshot |\n" +
		"| **bound directory** | a directory kae pin has bound |\n" +
		"| question | authority |\n" +
		"not a table row at all\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := contextTerms(path)
	if err != nil {
		t.Fatalf("contextTerms: %v", err)
	}
	want := map[string]bool{"account": true, "bound directory": true}
	for _, g := range got {
		if !want[g] {
			t.Errorf("contextTerms returned %q, which is a table header or not a term", g)
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

// The plural is load-bearing: the glossary names "reader" and the prose says
// "readers" more often, so matching the exact token alone would miss most of the
// subject the anchor exists for.
func TestSingleWordAnchorsMatchThePlural(t *testing.T) {
	paras := []paragraph{
		prepare(paragraph{file: "a.md", line: 1, words: normalize("the readers that named another account disagree about whose login this copy is")}),
	}
	buckets := indexAnchors(paras, map[string]bool{"reader": true})
	if len(buckets["reader"]) != 1 {
		t.Fatalf("anchor \"reader\" must match the plural in prose, got %v", buckets)
	}
	buckets = indexAnchors(paras, map[string]bool{"harvest": true})
	if len(buckets) != 0 {
		t.Fatalf("control: an absent anchor must match nothing, got %v", buckets)
	}
}
