package devtoolspolicy

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/tools/devtools/docrefs"
)

// TestSectionNumbersAreWrittenWithNoSpaceAfterTheSigil is the measurement the digit
// exclusion rests on, moved out of the package comment that used to quote it as a command.
// The comment quoted a `git grep` nobody re-ran, and it was measured false on 2026-08-14 in
// the way no re-run of a diff's own quoted commands can reach: the spaced form was added to
// a document in a commit that never opened this file, so the sentence recording the
// emptiness was falsified from outside its own diff and a reviewer's grep found it. Here it
// is the tree's property, checked by the thing that already re-runs the tree's properties.
//
// The text is read the way citeRe reads it, through stripFences and unwrap, and that is the
// difference that mattered against the `git grep` it replaces (which also read only tracked
// files, and did not prune `dist/`). A grep is line-oriented, so
// a citation that wraps after the sigil — which is where this repository's prose wraps, and
// why unwrap exists — reads as clean to it. Measured: a `§` at end of line with `6` opening
// the next passes a byte-level scan and is matched by a digit-admitting citeRe, so a
// byte-level test would have vouched for exactly the claim it cannot see. Stripping fences
// costs nothing and buys the other direction, since a fenced example is not a citation.
//
// Two-sided, because the arm that matters is a negative and a walk that reaches nothing
// satisfies it: no citation may be spaced, AND an unspaced one must be present. Both arms
// carry citeRe's own `.md` prefix — without it the negative flags ordinary prose numbering
// that citeRe can never match, and the positive is satisfied by an `RFC 6902 §4.1` that is
// not a citation either. This file writes both patterns as regexp source, so the sigil is
// followed by `[` and neither arm reads itself.
func TestSectionNumbersAreWrittenWithNoSpaceAfterTheSigil(t *testing.T) {
	root := repositoryRoot(t)
	// docFiles rather than a walk of its own: main() answers for which documents exist and
	// which stat failure is fatal, and a second copy of that policy is what this test's
	// first version got wrong.
	files, err := docrefs.Files(root)
	if err != nil {
		t.Fatalf("collecting documents under %s: %v", root, err)
	}
	var (
		spacedRe    = regexp.MustCompile("[A-Za-z0-9_./-]*\\.md[`'\")\\]]*[ \t]*§[ \t]+[0-9]")
		unspacedRe  = regexp.MustCompile("[A-Za-z0-9_./-]*\\.md[`'\")\\]]*[ \t]*§[0-9]")
		spaced      []string
		sawUnspaced bool
	)
	for _, p := range files {
		// The same two transforms main() applies before citeRe sees the text, so this
		// vouches for the set citeRe is actually applied to rather than for bytes on disk.
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("reading %s: %v", p, err)
		}
		b := []byte(docrefs.CitationText(string(raw)))
		rel, _ := filepath.Rel(root, p)
		for _, m := range spacedRe.FindAll(b, -1) {
			spaced = append(spaced, rel+": "+string(m))
		}
		sawUnspaced = sawUnspaced || unspacedRe.Match(b)
	}
	if !sawUnspaced {
		t.Fatal("no unspaced sigil-then-digit anywhere: the walk read nothing, so the negative below proves nothing")
	}
	if len(spaced) != 0 {
		t.Errorf("citeRe excludes a digit after the sigil because no live instance is spaced; these are:\n%s",
			strings.Join(spaced, "\n"))
	}
}

// TestTheCitedSkillSectionHasNoFirstWordRival holds a property the routes into
// .claude/skills/upstream-auth-drift/SKILL.md § Re-record rest on and that check-docs
// cannot. firstWordMatches compares only the cited name's first word, so another declared
// name in that file beginning `re-record` makes those citations resolve against the wrong
// thing, and the cited section can then be renamed away with every gate green.
//
// An allowlist of one, because the general form is unusable rather than merely stricter:
// most resolving citations in this tree already share a first word with more than one
// declared name in their target, and the reason is an idiom, not an accident — every
// heading in docs/CLI.md begins `kae`, so the commonest citation form here (`docs/CLI.md
// § kae <verb>`) has a rival for each of them. Filtering to citations whose sentence says
// "normative" was measured worse, not better. Re-derive both by comparing each cite row
// from this program against firstWordMatches over its target's names.
//
// Two arms, because counting declared names is one condition short: renaming the heading
// away *and* adding a `**Re-record …**` label in the same edit leaves the count at one, as
// does downgrading the heading to a list-item bold title, and both are green on a count
// alone. The count still has to run over sectionNames rather than headings, because a
// line-opening bold label and a list-item bold title are declared names too — that is the
// rival a heading grep cannot see.
//
// What it does not reach: the heading surviving with its section's content replaced. The
// citations would still resolve against a section reading TODO — AGENTS.md § Documentation
// Update Checklist owns that class, and it stays a reading task.
func TestTheCitedSkillSectionHasNoFirstWordRival(t *testing.T) {
	root := repositoryRoot(t)
	const rel = ".claude/skills/upstream-auth-drift/SKILL.md"
	const word = "re-record"
	raw, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("reading %s, which is cited as normative from outside the skill: %v", rel, err)
	}
	declared, headings := firstWordDeclaredAndHeadings(string(raw), word)
	if len(declared) != 1 {
		t.Errorf("%s declares %d names beginning `%s`, want exactly 1 — every `§ Re-record` "+
			"citation resolves on that first word alone, so none breaks them loudly and a "+
			"rival breaks them silently:\n%s",
			rel, len(declared), word, strings.Join(declared, "\n"))
	}
	if len(headings) == 0 {
		t.Errorf("%s declares no heading beginning `%s`, so a `§ Re-record` citation resolves "+
			"against a bold label and lands a reader nowhere — the section can be renamed away "+
			"from here with every gate green. Declared names beginning it:\n%s",
			rel, word, strings.Join(declared, "\n"))
	}
}

// ACCEPTANCE.md has two externally cited release surfaces. check-docs can only compare
// the first word of each cited name, so a shared first word lets either heading disappear
// while citations resolve against the other. Keep both words unique among every declared
// name, including line-opening bold labels, and require the declaration to stay a heading.
func TestTheCitedAcceptanceSectionsHaveNoFirstWordRival(t *testing.T) {
	root := repositoryRoot(t)
	const rel = "docs/ACCEPTANCE.md"
	raw, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("reading %s: %v", rel, err)
	}
	if defects := acceptanceSectionNameDefects(string(raw)); len(defects) != 0 {
		t.Fatalf("%s:\n%s", rel, strings.Join(defects, "\n"))
	}
	// Original broken shape: another declared name beginning Real-machine lets the
	// cited heading disappear while the first-word citation still resolves.
	mutant := string(raw) + "\n**Real-machine rival.** This line deliberately collides.\n"
	if defects := acceptanceSectionNameDefects(mutant); len(defects) == 0 {
		t.Fatal("a line-opening bold rival must make the uniqueness guard fail")
	}
}

func acceptanceSectionNameDefects(doc string) []string {
	var defects []string
	for _, word := range []string{"credential-expiry", "real-machine"} {
		declared, headings := firstWordDeclaredAndHeadings(doc, word)
		if len(declared) != 1 {
			defects = append(defects, fmt.Sprintf("declares %d names beginning `%s`, want exactly 1:\n%s",
				len(declared), word, strings.Join(declared, "\n")))
		}
		if len(headings) == 0 {
			defects = append(defects, fmt.Sprintf("declares no heading beginning `%s`; declared names:\n%s",
				word, strings.Join(declared, "\n")))
		}
	}
	return defects
}

// firstWordDeclaredAndHeadings returns every declared name beginning with word and the
// subset that remains a heading. The heading arm is intentionally only a lower bound: a
// rival heading is reported by the declared-name count, while absence means a citation
// resolves to a bold label or list item instead of a section.
func firstWordDeclaredAndHeadings(doc, word string) ([]string, []string) {
	matching := func(names [][]string) []string {
		var matches []string
		for _, name := range names {
			if name[0] == word {
				matches = append(matches, strings.Join(name, " "))
			}
		}
		return matches
	}
	return matching(docrefs.DeclaredNames(doc)), matching(docrefs.Headings(doc))
}

// repositoryRoot is two levels up from this package, asserted rather than assumed: `go test`
// runs with the package directory as the working directory and nothing here knows the root,
// so a package that moved would otherwise walk some other tree and pass.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving the repository root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("%s is not the repository root, so this test would vouch for the wrong tree: %v", root, err)
	}
	return root
}
