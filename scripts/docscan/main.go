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
//   - Calibration for whoever reads the next report: on 2026-08-10 every pair at
//     or above 0.25 involved docs/RELEASE.md restating a per-release status —
//     two consecutive entries carrying the same "still open and unchanged" block
//     word for word, and two release targets deferring the same list. That is
//     what a changelog is, not a fork. The finding worth acting on is a pair
//     spanning two *normative* documents.
//
// Anchors come from the Go AST rather than a regex because this repository's
// decision vocabulary lives in struct fields (Ordered, Conflicting,
// Unattributed) as much as in function names, and they are unioned with
// docs/CONTEXT.md's terms because the prose about a concept outnumbers the
// mentions of the symbol implementing it. Measured 2026-08-10 in the units this
// tool compares: of 927 of them, 5 name `credStoreReaders` and 28 use the word
// reader, so a symbol-only anchor set reaches under a fifth of the writing about
// that one concept.
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

	// The generated export of the shared Go CLI standard. Excluded for the same
	// reason AGENTS.md's documentation checklist excludes it: an edit there is
	// lost on the next re-sync, so a finding in it is not actionable here.
	generatedExport = ".claude/skills/go-cli-tooling/"
)

type paragraph struct {
	file     string
	line     int
	words    []string
	wordSet  map[string]bool
	joined   string
	shingles map[string]bool
	anchors  []string
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
			if len(p.words) < minWords {
				skipped++
				continue
			}
			paras = append(paras, prepare(p))
		}
	}

	buckets := indexAnchors(paras, anchors)
	pairs := comparePairs(paras, buckets)

	reportable := 0
	for _, pr := range pairs {
		if pr.score >= *minScore {
			reportable++
		}
	}

	fmt.Printf("docscan: %d anchors, %d paragraphs in %d documents, %d pairs at or above %.2f\n",
		len(anchors), len(paras), len(docs), reportable, *minScore)
	fmt.Printf("  skipped %d paragraphs under %d words\n", skipped, minWords)

	for i, pr := range pairs {
		if pr.score < *minScore {
			break
		}
		if i == *top {
			fmt.Printf("\n  (%d more at or above %.2f; raise -top to see them)\n",
				reportable-i, *minScore)
			break
		}
		a, b := paras[pr.a], paras[pr.b]
		fmt.Printf("\n%.2f  %s:%d  <->  %s:%d\n      shared anchors: %s\n",
			pr.score, a.file, a.line, b.file, b.line, strings.Join(pr.anchors, " "))
	}
	if reportable == 0 {
		fmt.Printf("\nno pair scored %.2f or above. That is not a statement about whether the\n"+
			"documents are correct — see this command's header.\n", *minScore)
	}
}

func prepare(p paragraph) paragraph {
	p.wordSet = make(map[string]bool, len(p.words))
	for _, w := range p.words {
		p.wordSet[w] = true
	}
	p.joined = " " + strings.Join(p.words, " ") + " "
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
			return skipDir(path, d)
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

// skipDir keeps the walk out of the places a finding would be useless: git's own
// tree, build output, and the generated export. `.claude` itself is walked, because
// the upstream-auth-drift skill under it is cited as normative and its prose can
// fork from docs/ like any other.
func skipDir(path string, d fs.DirEntry) error {
	switch {
	case path == ".":
		return nil
	case d.Name() == ".git", d.Name() == "dist":
		return fs.SkipDir
	case strings.HasPrefix(filepath.ToSlash(path)+"/", generatedExport):
		return fs.SkipDir
	default:
		return nil
	}
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

var contextRow = regexp.MustCompile(`^\|\s*\*{0,2}` + "`?" + `([A-Za-z][A-Za-z ._-]*?)` + "`?" + `\*{0,2}\s*\|`)

// contextTerms reads the first cell of every table row in the glossary. It is
// deliberately forgiving about the emphasis and backticks around a term, and
// deliberately ignorant of the rest of the row: this reads names, not rules.
func contextTerms(path string) ([]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(body), "\n") {
		m := contextRow.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		term := strings.TrimSpace(m[1])
		if len(term) >= 4 && term != "term" && term != "question" {
			out = append(out, term)
		}
	}
	return out, nil
}

// markdownFiles returns the same set AGENTS.md's documentation checklist derives:
// every markdown file except the generated export. It walks rather than shelling
// out to `git ls-files`, so an untracked draft is included — which is what you
// want from a tool run before a commit.
func markdownFiles() ([]string, error) {
	var out []string
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return skipDir(path, d)
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
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
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
// everything measured 3.4s wall over 926 paragraphs.
//
// ponytail: O(bucket²) per anchor, and the biggest bucket is most of the corpus.
// If this ever gets slow, band the shingle sets (minhash) rather than throwing
// anchors away — throwing anchors away is what silently lost the finding.
func indexAnchors(paras []paragraph, anchors map[string]bool) map[string][]int {
	type probe struct {
		anchor string
		word   string // set for a single-word anchor, "" for a phrase
		phrase string // set for a phrase anchor
	}
	probes := make([]probe, 0, len(anchors))
	for a := range anchors {
		la := strings.ToLower(a)
		if strings.ContainsAny(la, " .-_") {
			probes = append(probes, probe{anchor: a, phrase: " " + strings.Join(normalize(la), " ") + " "})
			continue
		}
		probes = append(probes, probe{anchor: a, word: la})
	}

	buckets := map[string][]int{}
	for i := range paras {
		for _, pr := range probes {
			hit := false
			switch {
			case pr.word != "":
				// A term is matched with its plural too: the glossary names "reader"
				// and the prose says "readers" more often than not.
				hit = paras[i].wordSet[pr.word] || paras[i].wordSet[pr.word+"s"]
			default:
				hit = strings.Contains(paras[i].joined, pr.phrase)
			}
			if hit {
				buckets[pr.anchor] = append(buckets[pr.anchor], i)
				paras[i].anchors = append(paras[i].anchors, pr.anchor)
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
	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		if out[i].a != out[j].a {
			return out[i].a < out[j].a
		}
		return out[i].b < out[j].b
	})
	return out
}
