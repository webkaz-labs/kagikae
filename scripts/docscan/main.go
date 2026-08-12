// Command docscan reports prose that two of this repository's documents carry
// twice, anchored on the identifiers the code actually declares.
//
// It is stage 2 of the four-stage docs scan used here — (1) an identifier index,
// (2) duplication, (3) claim reconciliation, (4) running the executable blocks
// verbatim — and the four stages do not have equal yield, so be exact about what
// a clean report buys:
//
//   - This measurement has produced real consolidations. The 2026-08-08 pass
//     found docs/SCOPE-MODEL.md and docs/ADAPTERS.md both carrying the claude
//     identity-cache TTL finding in full, the largest same-mechanism overlap
//     between any two documents here (commit f04d15e); duplicates kept on
//     purpose were recorded with their reasons rather than merged (621217f).
//   - It has never found a docs defect that would have broken a release. Every
//     one of those came from stage 4, running the blocks in docs/VALIDATION.md
//     line by line. Duplication and falsehood are different questions: this tool
//     cannot see a claim that is wrong in the only place it appears, and a clean
//     report is not evidence that the documents are true.
//   - Calibration for whoever reads the next report: on 2026-08-10 it found five
//     pairs at or above 0.25. Four are docs/RELEASE.md restating a per-release
//     status — two consecutive entries carrying the same "still open and
//     unchanged" block word for word, and two release targets deferring the same
//     list — which is what a changelog is, not a fork. The fifth is the shape
//     worth reading, and an earlier version of this paragraph swept it into the
//     other four: docs/PRODUCT.md's § Switching Surface table against the copy
//     frozen in the **v0.7.2** entry of docs/RELEASE.md, which has since diverged
//     and still names two retired mode names. A frozen changelog entry may be the
//     right answer; a *universal* about the report is how a reader stops looking.
//     The version in that sentence read v0.8.0 until it was checked against the
//     enclosing `# kae vX.Y.Z` heading — a reviewer and I had it wrong the same way,
//     which is the whole argument for re-deriving a detail before writing it down.
//
// Anchors come from the Go AST rather than a regex because this repository's
// decision vocabulary lives in struct fields (Ordered, Conflicting,
// Unattributed) as much as in function names, and they are unioned with
// docs/CONTEXT.md's terms because the prose about a concept outnumbers the
// mentions of the symbol implementing it. Measured 2026-08-10 over the units this
// tool compares: the ones naming `credStoreReaders` are under a fifth of the ones
// using the word *reader*, so a symbol-only anchor set reaches under a fifth of the
// writing about that one concept. The ratio is the claim; the two counts behind it
// move with every documentation edit, so they are deliberately not written here —
// one of them was already stale within the hour.
//
// Report-only, and deliberately not part of `mise run check`: it reports overlap
// for a human to judge, and nothing here should fail a commit. Run it from the
// repository root with `mise run docs-scan`.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	shingleN = 6 // words per shingle; 6 is what the 2026-08-08 pass used

	// A paragraph shorter than this cannot produce a shingle set worth comparing:
	// short table cells match each other word-for-word often enough to bury the
	// report. The number skipped is printed, never dropped silently.
	minWords = 25
)

type paragraph struct {
	file     string
	line     int
	words    []string
	wordSet  map[string]bool
	shingles map[string]bool
}

func main() {
	minScore := flag.Float64("min", 0.25, "report pairs at or above this Jaccard score")
	top := flag.Int("top", 40, "print at most this many pairs")
	flag.Parse()

	anchors, err := collectAnchors()
	if err != nil {
		fmt.Fprintln(os.Stderr, "docscan:", err)
		os.Exit(1)
	}
	docs, err := markdownFiles()
	if err != nil {
		fmt.Fprintln(os.Stderr, "docscan:", err)
		os.Exit(1)
	}

	var paras []paragraph
	skipped := 0
	for _, f := range docs {
		body, readErr := os.ReadFile(f)
		if readErr != nil {
			fmt.Fprintln(os.Stderr, "docscan:", readErr)
			os.Exit(1)
		}
		for _, p := range paragraphs(f, string(body)) {
			if !comparable(p) {
				skipped++
				continue
			}
			paras = append(paras, prepare(p))
		}
	}

	buckets := indexAnchors(paras, anchors)
	pairs := comparePairs(paras, buckets)

	// comparePairs returns them sorted descending, so the reportable ones are a
	// prefix. Slicing rather than counting while printing is deliberate: the first
	// version derived "how many more are there" from the loop index, and a review
	// killed three separate off-by-one mutants of that arithmetic that no test saw.
	report := pairs[:countAtLeast(pairs, *minScore)]
	shown := min(*top, len(report))

	fmt.Printf("docscan: %d anchors, %d paragraphs in %d documents, %d pairs at or above %.2f\n",
		len(anchors), len(paras), len(docs), len(report), *minScore)
	fmt.Printf("  skipped %d paragraphs under %d words\n", skipped, minWords)

	for _, pr := range report[:shown] {
		a, b := paras[pr.a], paras[pr.b]
		fmt.Printf("\n%.2f  %s:%d  <->  %s:%d\n      shared anchors: %s\n",
			pr.score, a.file, a.line, b.file, b.line, strings.Join(pr.anchors, " "))
	}
	if rest := len(report) - shown; rest > 0 {
		fmt.Printf("\n  (%d more at or above %.2f; raise -top to see them)\n", rest, *minScore)
	}
	if len(report) == 0 {
		fmt.Printf("\nno pair scored %.2f or above. That is not a statement about whether the\n"+
			"documents are correct — see this command's header.\n", *minScore)
	}
}

// comparable reports whether a unit carries enough words to be worth scoring.
func comparable(p paragraph) bool { return len(p.words) >= minWords }

// countAtLeast returns how many of a descending-sorted slice reach min.
func countAtLeast(pairs []pair, min float64) int {
	for i, pr := range pairs {
		if pr.score < min {
			return i
		}
	}
	return len(pairs)
}

func prepare(p paragraph) paragraph {
	p.wordSet = make(map[string]bool, len(p.words))
	for _, w := range p.words {
		p.wordSet[w] = true
	}
	p.shingles = shingles(p.words, shingleN)
	return p
}

// collectAnchors returns the identifiers to bucket paragraphs on: every name the
// Go sources declare, unioned with the terms docs/CONTEXT.md names.
func collectAnchors() (map[string]bool, error) {
	anchors := map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return skipDir(path)
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if perr != nil {
			return fmt.Errorf("parse %s: %w", path, perr)
		}
		for _, name := range declaredNames(f) {
			anchors[name] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	terms, err := contextTerms(filepath.Join("docs", "CONTEXT.md"))
	if err != nil {
		return nil, err
	}
	for _, t := range terms {
		anchors[t] = true
	}
	return anchors, nil
}

// excludedDir keeps the walk out of the places a finding would be useless: git's
// own tree and build output. `.claude` itself is walked, because the
// upstream-auth-drift skill under it is cited as normative and its prose can fork
// from docs/ like any other. It used to skip a third place, the generated export of
// the shared Go CLI standard under `.claude/skills/go-cli-tooling/`, where a finding
// was unactionable because the next re-sync would drop the edit; that export is gone
// and the standard is read from the user-level skill instead.
//
// A predicate rather than an inline switch so a test can pin it: this walk is what
// makes the tool's document set equal the one AGENTS.md's checklist derives, and
// that equality was verified once by hand, which is not a guard.
func excludedDir(path string) bool {
	slash := filepath.ToSlash(path)
	if slash == "." {
		return false
	}
	base := slash
	if i := strings.LastIndex(slash, "/"); i >= 0 {
		base = slash[i+1:]
	}
	return base == ".git" || base == "dist"
}

func skipDir(path string) error {
	if excludedDir(path) {
		return fs.SkipDir
	}
	return nil
}

// declaredNames returns every func, type, struct field, const and var name one
// file declares. Struct fields are in here on purpose: this repository decides
// things with them (Ordered, Conflicting, ForeignToReaders), and an anchor set of
// funcs and constants alone measured well under half of this one.
func declaredNames(f *ast.File) []string {
	var out []string
	add := func(id *ast.Ident) {
		if id != nil && id.Name != "_" && len(id.Name) >= 4 {
			out = append(out, id.Name)
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch d := n.(type) {
		case *ast.FuncDecl:
			add(d.Name)
		case *ast.TypeSpec:
			add(d.Name)
		case *ast.ValueSpec:
			for _, id := range d.Names {
				add(id)
			}
		case *ast.Field:
			for _, id := range d.Names {
				add(id)
			}
		}
		return true
	})
	return out
}

// termSections are the two headings in docs/CONTEXT.md whose tables define terms.
// The section check is load-bearing rather than tidy: the glossary opens with a
// routing table whose first column is a *question* ("what a decision does"), and a
// row-shape-only reader harvested all five of those as vocabulary.
var termSections = map[string]bool{"Surface terms": true, "Mechanism terms": true}

var (
	sectionLine   = regexp.MustCompile(`^##\s+(.*?)\s*$`)
	parenthetical = regexp.MustCompile(`\([^)]*\)`)
	termShape     = regexp.MustCompile(`^[A-Za-z][A-Za-z ._-]*$`)
)

// contextTerms reads the terms out of the glossary's own term tables: the first
// cell of each row, with emphasis and backticks stripped, parentheticals dropped,
// and a comma-separated cell read as the several terms it is. It stays ignorant of
// the rest of the row on purpose — this reads names, not rules.
//
// Requiring the whole cell to be one bare name is what the first version did, and
// it silently skipped `**mode** (`shared`, `isolated`)` and `**supersedes**,
// **orderable**`. Silently is the problem: they were still reachable from the Go
// AST, so nothing looked wrong, and the first glossary-only term to acquire a
// parenthetical would have vanished with no signal.
func contextTerms(path string) ([]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	section := ""
	for _, line := range strings.Split(string(body), "\n") {
		if m := sectionLine.FindStringSubmatch(line); m != nil {
			section = m[1]
			continue
		}
		if !termSections[section] || !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		cells := strings.Split(strings.TrimSpace(line), "|")
		if len(cells) < 2 {
			continue
		}
		cell := parenthetical.ReplaceAllString(cells[1], " ")
		for _, piece := range strings.Split(cell, ",") {
			term := strings.TrimSpace(strings.Trim(strings.TrimSpace(piece), "*`"))
			if len(term) >= 4 && term != "term" && termShape.MatchString(term) {
				out = append(out, term)
			}
		}
	}
	return out, nil
}

// markdownFiles returns the same set AGENTS.md's documentation checklist derives:
// every markdown file in the tree. It walks rather than shelling out to
// `git ls-files`, so an untracked draft is included — which is what you want from a
// tool run before a commit.
func markdownFiles() ([]string, error) {
	var out []string
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return skipDir(path)
		}
		if strings.HasSuffix(path, ".md") {
			out = append(out, filepath.ToSlash(path))
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

var wordRe = regexp.MustCompile(`[A-Za-z0-9_]+`)

// paragraphs splits one markdown document into comparable units. A table row is
// its own unit rather than part of one block, because several documents here carry
// a mechanism's whole description in one row. Fenced code is skipped: two copies of
// the same command are meant to be identical.
func paragraphs(file, body string) []paragraph {
	var out []paragraph
	var cur []string
	start := 0
	inFence := false

	flush := func() {
		if len(cur) == 0 {
			return
		}
		out = append(out, paragraph{file: file, line: start, words: normalize(strings.Join(cur, " "))})
		cur = nil
	}

	for i, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			flush()
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		switch {
		case trimmed == "", strings.HasPrefix(trimmed, "#"):
			flush()
		case strings.HasPrefix(trimmed, "|"):
			flush()
			out = append(out, paragraph{file: file, line: i + 1, words: normalize(trimmed)})
		default:
			if len(cur) == 0 {
				start = i + 1
			}
			cur = append(cur, trimmed)
		}
	}
	flush()
	return out
}

func normalize(text string) []string {
	return wordRe.FindAllString(strings.ToLower(text), -1)
}

func shingles(words []string, n int) map[string]bool {
	out := map[string]bool{}
	for i := 0; i+n <= len(words); i++ {
		out[strings.Join(words[i:i+n], " ")] = true
	}
	return out
}

func jaccard(a, b map[string]bool) float64 {
	shared := 0
	for s := range a {
		if b[s] {
			shared++
		}
	}
	if shared == 0 {
		return 0
	}
	return float64(shared) / float64(len(a)+len(b)-shared)
}

// indexAnchors assigns each paragraph the anchors it mentions and returns the
// anchor -> paragraph buckets.
//
// Every anchor is kept, including the ubiquitous ones. An earlier version skipped
// anchors appearing in over 5% of paragraphs, on the theory that they bucket the
// corpus together and narrow nothing — and it hid the highest-scoring pair in the
// tree, an exact 1.00 match, because the anchors that pair shared were `account`
// and the tool names. There is no cost argument for the filter either: keeping
// every anchor costs a few seconds over the whole corpus — measure it rather than
// trusting a figure that rots, with `time go run ./scripts/docscan`.
//
// ponytail: O(bucket²) per anchor, and the biggest bucket is most of the corpus.
// If this ever gets slow, band the shingle sets (minhash) rather than throwing
// anchors away — throwing anchors away is what silently lost the finding.
func indexAnchors(paras []paragraph, anchors map[string]bool) map[string][]int {
	// One rule for every anchor: it hits a paragraph when each of its words is
	// present, singular or plural. A symbol like credStoreReaders is the one-word
	// case of it, and a glossary phrase like "bound directory" is the two-word one.
	//
	// The first version instead matched a phrase as an adjacent substring and only
	// gave the plural to single words, which two reviews caught between them: the
	// substring form cannot match "bound directories" at all, and *every* phrase
	// anchor turned out to contribute no pair the single-word anchors did not
	// already contribute — so the half of the anchor set that justified reading
	// docs/CONTEXT.md was both narrower than the prose and doing nothing. Requiring
	// the words without requiring adjacency is looser, and loose is the right
	// direction here: an anchor only decides which paragraphs to compare, and the
	// score decides what to report.
	probes := make([][]string, 0, len(anchors))
	names := make([]string, 0, len(anchors))
	for a := range anchors {
		probes = append(probes, normalize(a))
		names = append(names, a)
	}

	buckets := map[string][]int{}
	for i := range paras {
		for p, words := range probes {
			if len(words) == 0 {
				continue
			}
			hit := true
			for _, w := range words {
				if !paras[i].wordSet[w] && !paras[i].wordSet[w+"s"] {
					hit = false
					break
				}
			}
			if hit {
				buckets[names[p]] = append(buckets[names[p]], i)
			}
		}
	}

	return buckets
}

type pair struct {
	a, b    int
	score   float64
	anchors []string
}

// comparePairs scores every pair of paragraphs sharing at least one anchor. The
// shared anchor is what makes this affordable and what makes a hit worth reading:
// two paragraphs with no anchor in common are not describing the same mechanism,
// however similar their wording.
func comparePairs(paras []paragraph, buckets map[string][]int) []pair {
	type key struct{ a, b int }
	seen := map[key]*pair{}
	names := make([]string, 0, len(buckets))
	for a := range buckets {
		names = append(names, a)
	}
	sort.Strings(names)
	for _, anchor := range names {
		idx := buckets[anchor]
		for i := 0; i < len(idx); i++ {
			for j := i + 1; j < len(idx); j++ {
				a, b := idx[i], idx[j]
				if a > b {
					a, b = b, a
				}
				k := key{a, b}
				if p, ok := seen[k]; ok {
					p.anchors = append(p.anchors, anchor)
					continue
				}
				seen[k] = &pair{
					a: a, b: b,
					score:   jaccard(paras[a].shingles, paras[b].shingles),
					anchors: []string{anchor},
				}
			}
		}
	}
	out := make([]pair, 0, len(seen))
	for _, p := range seen {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return pairLess(out[i], out[j]) })
	return out
}

// pairLess is the report's order: highest score first, then by paragraph index so
// equal scores come out the same way every run.
//
// A named function rather than a closure because a comparator cannot be pinned
// through a sort of three elements — a review removed the index tiebreak and the
// test that asserts the order still passed, since with two equal elements an
// unstable sort happens to leave them alone.
func pairLess(a, b pair) bool {
	if a.score != b.score {
		return a.score > b.score
	}
	if a.a != b.a {
		return a.a < b.a
	}
	return a.b < b.b
}
