package l10n

import (
	"go/importer"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The catalog checks hold vacuously while the catalog is empty, so each one is
// shown failing on a bad entry here.

func TestVerbsAgreeComparesArgumentsAndVerbs(t *testing.T) {
	cases := []struct {
		en, ja string
		want   bool
	}{
		{"cannot read %s: %v", "%s を読めません: %v", true},
		{"%s/%s is active", "%[2]s の %[1]s が有効です", true}, // reordered by explicit indices
		{"%s then %d", "%[2]d の後に %[1]s", true},
		{"%s then %d", "%d の後に %s", false},
		{"one %s", "ひとつ", false},
		{"one %s", "%s と %s", false},
		{"%q here", "%s です", false},
		{"%-10s|%d", "%-12s|%d", true}, // width may differ
		{"%*d", "%*d", true},
		{"100%% sure %s", "%s は確実です", true},
		{"wrap: %w", "包む: %w", true},
		{"wrap: %w", "包む: %v", false},
	}
	for _, tc := range cases {
		if got := verbsAgree(tc.en, tc.ja); got != tc.want {
			t.Errorf("verbsAgree(%q, %q) = %v, want %v", tc.en, tc.ja, got, tc.want)
		}
	}
}

func TestAmbiguousRunesFollowsTheWidthTable(t *testing.T) {
	for _, s := range []string{"設定は変更しません。", "アカウント・プロファイル", "（例: 1 つ）", "100％", "kae: warning: x"} {
		if rs := ambiguousRunes(s); len(rs) > 0 {
			t.Errorf("%q reported ambiguous %q", s, string(rs))
		}
	}
	for _, s := range []string{"a → b", "· 区切り", "省略…", "—", "×3", "※注", "①", "“引用”"} {
		if rs := ambiguousRunes(s); len(rs) == 0 {
			t.Errorf("%q has an East Asian Ambiguous character the check missed", s)
		}
	}
}

func TestHasProseSkipsDirectivesAndLinePrefixes(t *testing.T) {
	for _, s := range []string{"%s\n", "  %-*s %s\n", " · ", "kae:", "kae: warning: %v\n", "kae: note: %s", "%[2]q=%[1]d", "-"} {
		if hasProse(s) {
			t.Errorf("hasProse(%q) = true, want false", s)
		}
	}
	for _, s := range []string{"Created %s\n", "kae: warning: could not %s", "kae: harvested %s", "  warning: %s\n", "%s places:\n"} {
		if !hasProse(s) {
			t.Errorf("hasProse(%q) = false, want true", s)
		}
	}
}

// methodSinkFixture has a method sink that forwards its format to another
// method sink, the shape of App.acquireNamedLock and App.acquireNamed.
const methodSinkFixture = `package fx

import "errors"

type App struct{}

func (a *App) wrap(name, format string, args ...any) error { return errors.New(format) }

func (a *App) wrapLock(format string, args ...any) error { return a.wrap("n", format, args...) }

func use(a *App, s string) {
	_ = a.wrapLock("const " + "%s", "x")
	_ = a.wrap("n", s)
	_ = errors.New("plain")
}
`

// scanFixture type-checks methodSinkFixture as package fx with the given sinks.
func scanFixture(t *testing.T, sinks map[string]int) *scan {
	t.Helper()
	s := newScan(t.TempDir(), sinks)
	checkFixture(t, s, methodSinkFixture)
	return s
}

// checkFixture writes src as package fx in s.root and checks it with s.
func checkFixture(t *testing.T, s *scan, src string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(s.root, "fx.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	pkg := listedPackage{ImportPath: "fx", Dir: s.root, GoFiles: []string{"fx.go"}}
	if err := s.checkPackage(fset, importer.ForCompiler(fset, "source", nil), pkg); err != nil {
		t.Fatal(err)
	}
}

func findingSummary(s *scan) []string {
	var out []string
	for _, f := range s.findings {
		out = append(out, f.fn+" "+f.kind)
	}
	slices.Sort(out)
	return out
}

func TestMethodSinksAreJudgedAtTheirCallersAndSkippedInTheirBodies(t *testing.T) {
	s := scanFixture(t, map[string]int{"fx.App.wrap": 1, "fx.App.wrapLock": 0})
	// The bodies of wrap and wrapLock forward their format and are skipped; the
	// callers are judged: a constant format not in the catalog, a non-constant one,
	// and an error that is not a message value.
	want := []string{"use error", "use sink", "use sink"}
	if got := findingSummary(s); !slices.Equal(got, want) {
		t.Fatalf("findings = %q, want %q", got, want)
	}
	if !s.used["const %s"] {
		t.Fatalf("the constant format %q must be recorded as used", "const %s")
	}

	// Without the registration, the bodies are ordinary code and the callers are
	// not sinks: what is left is the two errors.New calls.
	s = scanFixture(t, map[string]int{})
	want = []string{"App.wrap error", "use error"}
	if got := findingSummary(s); !slices.Equal(got, want) {
		t.Fatalf("unregistered findings = %q, want %q", got, want)
	}
}

// flagSetFixture calls flag.NewFlagSet from the allowed maker and from elsewhere.
const flagSetFixture = `package fx

import "flag"

func newFlagSet(name string) *flag.FlagSet { return flag.NewFlagSet(name, flag.ContinueOnError) }

func elsewhere() *flag.FlagSet { return flag.NewFlagSet("x", flag.ContinueOnError) }
`

func TestOnlyTheFlagSetMakerCallsNewFlagSet(t *testing.T) {
	s := newScan(t.TempDir(), map[string]int{})
	s.flagSetMaker = "fx.newFlagSet"
	checkFixture(t, s, flagSetFixture)
	if len(s.strayFlagSets) != 1 || !strings.HasSuffix(s.strayFlagSets[0], "fx.go:7:41") {
		t.Fatalf("stray flag sets = %q, want the one in elsewhere", s.strayFlagSets)
	}
}

// backquoteFixture registers a description with a backquote in English, one whose
// Japanese holds one, and a clean one.
const backquoteFixture = `package fx

import "flag"

func register(fs *flag.FlagSet) {
	fs.Bool("a", false, "run ` + "`kae use`" + `")
	fs.Bool("b", false, "japanese quotes")
	fs.Bool("c", false, "plain")
}
`

func TestBackquotedFlagDescriptionsAreFoundInEitherLanguage(t *testing.T) {
	restore := UseCatalogForTest(map[string]string{
		"run `kae use`":   "kae use を実行",
		"japanese quotes": "`kae use` を実行",
		"plain":           "そのまま",
	})
	t.Cleanup(restore)
	s := newScan(t.TempDir(), map[string]int{})
	checkFixture(t, s, backquoteFixture)
	if len(s.backquotedFlags) != 2 ||
		!strings.Contains(s.backquotedFlags[0], "\"run `kae use`\"") ||
		!strings.Contains(s.backquotedFlags[1], `"japanese quotes"`) {
		t.Fatalf("backquoted flags = %q, want a and b", s.backquotedFlags)
	}
}

// concatenatedPrintFixture prints literals whole, inside a concatenation, inside
// a parenthesised constant operand of one, and as the argument of another call.
const concatenatedPrintFixture = `package fx

import (
	"fmt"
	"os"
	"strings"
)

func help(items []string) {
	fmt.Println("Usage:\n  run it\n\nTools: " + strings.Join(items, ", "))
	fmt.Fprintln(os.Stdout, strings.Join(items, ", ")+(" and "+"more"))
	fmt.Println(strings.Join(items, ", ") + ", " + "\n")
	fmt.Println(strings.ToUpper("quiet words"))
	fmt.Print("whole " + "constant")
}
`

func TestPrintedConcatenationsAreReadForProse(t *testing.T) {
	s := newScan(t.TempDir(), map[string]int{})
	checkFixture(t, s, concatenatedPrintFixture)
	var got []string
	for _, f := range s.findings {
		got = append(got, f.kind+" "+f.what)
	}
	want := []string{
		`print fmt.Println writes "Usage:\n  run it\n\nTools: "`,
		`print fmt.Fprintln writes " and more"`,
		`print fmt.Print writes "whole constant"`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("findings = %q, want %q", got, want)
	}
}
