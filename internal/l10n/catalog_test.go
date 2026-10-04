package l10n

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode"

	"golang.org/x/text/width"
)

// The catalog test of docs/VALIDATION.md § Output language in tests. It reads the
// source of every package in the kae binary, type-checked so a format argument is
// judged by its constant value ("a" + "b" counts) rather than by its spelling, and
// fails on the conditions that section lists. What is not migrated yet is on the
// second allowlist (allowlist_test.go), which can only shrink: its counts must
// equal what the source holds, so a migrated call fails the test until its entry
// is lowered, and a new unmigrated call fails it until the message is put in the
// catalog.
//
// What it does not see: a literal printed through anything but fmt's print
// functions (io.WriteString, a Bubble Tea view), English composed with
// fmt.Sprintf and printed later, a constant English argument handed to a sink
// (it cannot tell prose from a token), and files outside this platform's build
// (their allowlist entries are skipped, not checked; a literal format they pass
// to a sink still counts as used, so a key used only on another shipped platform
// is not an orphan). The package list comes from `go list -deps` of the kae binary.

const (
	modulePath = "github.com/webkaz-labs/kagikae"
	cmdPkg     = modulePath + "/internal/cmd"
	l10nPkg    = modulePath + "/internal/l10n"
)

// formatSinks are the functions whose format argument must be a constant in the
// catalog: the writing sinks, the constructors of message values and the
// functions that forward a format to one of them. A call inside a sink's own
// body forwards its format parameter and is not judged. Keyed by funcKey:
// "pkg.Func", or "pkg.Recv.Method" for a method.
var formatSinks = map[string]int{ // key -> format argument index
	cmdPkg + ".errf":                       1,
	cmdPkg + ".usageError":                 0,
	cmdPkg + ".msgf":                       0,
	cmdPkg + ".warnf":                      0,
	cmdPkg + ".notef":                      0,
	cmdPkg + ".infof":                      0,
	cmdPkg + ".reportf":                    0,
	cmdPkg + ".promptf":                    0,
	cmdPkg + ".stderrf":                    0,
	cmdPkg + ".App.acquireNamed":           2,
	cmdPkg + ".App.acquireNamedLock":       1,
	cmdPkg + ".App.acquireNamedSharedLock": 1,
	l10nPkg + ".Sprintf":                   0,
	l10nPkg + ".Msgf":                      0,
	l10nPkg + ".Errorf":                    0,
}

// flagUsageArg is the index of the usage argument of each flag registration, the
// same for the package-level function and the *flag.FlagSet method.
var flagUsageArg = map[string]int{
	"Bool": 2, "Int": 2, "Int64": 2, "Uint": 2, "Uint64": 2, "String": 2, "Float64": 2, "Duration": 2,
	"BoolVar": 3, "IntVar": 3, "Int64Var": 3, "UintVar": 3, "Uint64Var": 3, "StringVar": 3,
	"Float64Var": 3, "DurationVar": 3, "TextVar": 3,
	"Var":  2,
	"Func": 1, "BoolFunc": 1,
}

// printFuncs are the fmt calls that write; Fprint* take the writer first.
var printFuncs = map[string]int{ // name -> index of the first printed argument
	"Print": 0, "Printf": 0, "Println": 0,
	"Fprint": 1, "Fprintf": 1, "Fprintln": 1,
}

// Finding kinds, the columns of the second allowlist.
const (
	kindSink  = "sink"  // a sink's format is not a constant in the catalog
	kindPrint = "print" // a fmt print call writes a literal outside the catalog
	kindFlag  = "flag"  // a flag description is not in the catalog
	kindError = "error" // fmt.Errorf / errors.New in internal/cmd or below, not yet a value
)

type finding struct {
	file string // relative to the module root
	fn   string // enclosing function: "Name" or "Recv.Name"; "" at package level
	kind string
	pos  string
	what string
}

type scan struct {
	root     string         // the module root the files are relative to
	sinks    map[string]int // formatSinks, or a test's own
	findings []finding
	// used holds every constant format a sink or flag registration passes, so a
	// catalog key nobody passes is an orphan.
	used map[string]bool
	// files is every file in the build, relative to the module root.
	files map[string]bool
	// flagSetMaker is the one function allowed to call flag.NewFlagSet, as
	// "pkg.Func"; strayFlagSets are the positions of every other call.
	flagSetMaker  string
	strayFlagSets []string
	pkg           string // the package being checked
}

// flagSetMaker is the function every flag.FlagSet comes from: it silences the
// flag package's own printing so kae renders parse failures and usage
// (docs/CLI.md § Localization, "The `flag` package").
const flagSetMaker = cmdPkg + ".newFlagSet"

func newScan(root string, sinks map[string]int) *scan {
	return &scan{root: root, sinks: sinks, used: map[string]bool{}, files: map[string]bool{}, flagSetMaker: flagSetMaker}
}

// scanOnce type-checks the kae binary's packages once per test process.
var scanOnce = sync.OnceValues(runScan)

func scanSource(t *testing.T) *scan {
	t.Helper()
	s, err := scanOnce()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type listedPackage struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	Export     string
	Standard   bool
}

func runScan() (*scan, error) {
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		return nil, fmt.Errorf("go list -m: %w", err)
	}
	root := strings.TrimSpace(string(out))
	list := exec.Command("go", "list", "-deps", "-export",
		"-json=ImportPath,Dir,GoFiles,Export,Standard", modulePath)
	list.Dir = root
	var stderr bytes.Buffer
	list.Stderr = &stderr
	out, err = list.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -deps -export: %w\n%s", err, stderr.String())
	}
	var pkgs []listedPackage
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p listedPackage
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode go list: %w", err)
		}
		pkgs = append(pkgs, p)
	}
	exports := map[string]string{}
	for _, p := range pkgs {
		exports[p.ImportPath] = p.Export
	}
	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		file, ok := exports[path]
		if !ok || file == "" {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(file)
	})
	s := newScan(root, formatSinks)
	for _, p := range pkgs {
		if !inModule(p) {
			continue
		}
		if err := s.checkPackage(fset, imp, p); err != nil {
			return nil, err
		}
	}
	if err := s.addOtherPlatformUses(); err != nil {
		return nil, err
	}
	return s, nil
}

func inModule(p listedPackage) bool {
	return !p.Standard && (p.ImportPath == modulePath || strings.HasPrefix(p.ImportPath, modulePath+"/"))
}

// shippedPlatforms are the GOOS values kae is built for. A catalog key passed
// only from a file of another platform's build is not an orphan.
var shippedPlatforms = []string{"darwin", "linux"}

// addOtherPlatformUses marks the keys passed from files outside this build that
// another shipped platform compiles. Without export data for that platform the
// files are read by syntax alone: a string literal in a sink's format position,
// the sink named as the call spells it (a bare name in its own package, the
// package's last path element otherwise, any receiver for a method).
func (s *scan) addOtherPlatformUses() error {
	for _, goos := range shippedPlatforms {
		if goos == runtime.GOOS {
			continue // already in s.files
		}
		list := exec.Command("go", "list", "-deps", "-json=ImportPath,Dir,GoFiles,Standard", modulePath)
		list.Dir = s.root
		list.Env = append(os.Environ(), "GOOS="+goos)
		var stderr bytes.Buffer
		list.Stderr = &stderr
		out, err := list.Output()
		if err != nil {
			return fmt.Errorf("GOOS=%s go list -deps: %w\n%s", goos, err, stderr.String())
		}
		dec := json.NewDecoder(bytes.NewReader(out))
		for {
			var p listedPackage
			if err := dec.Decode(&p); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				return fmt.Errorf("decode go list: %w", err)
			}
			if !inModule(p) {
				continue
			}
			for _, name := range p.GoFiles {
				path := filepath.Join(p.Dir, name)
				rel, err := filepath.Rel(s.root, path)
				if err != nil {
					return err
				}
				if s.files[filepath.ToSlash(rel)] {
					continue
				}
				if err := s.addSyntacticUses(p.ImportPath, path); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *scan) addSyntacticUses(pkg, path string) error {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		return err
	}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		for key, index := range s.sinks {
			if index >= len(call.Args) || !spellsSink(pkg, key, call.Fun) {
				continue
			}
			if lit, ok := call.Args[index].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if format, err := strconv.Unquote(lit.Value); err == nil {
					s.used[format] = true
				}
			}
		}
		return true
	})
	return nil
}

// spellsSink reports whether fun, written in package pkg, names the sink key.
func spellsSink(pkg, key string, fun ast.Expr) bool {
	sinkPkg, name, _ := strings.Cut(strings.TrimPrefix(key, modulePath+"/"), ".")
	sinkPkg = modulePath + "/" + sinkPkg
	_, method, isMethod := strings.Cut(name, ".")
	switch fun := ast.Unparen(fun).(type) {
	case *ast.Ident:
		return !isMethod && pkg == sinkPkg && fun.Name == name
	case *ast.SelectorExpr:
		if isMethod {
			return pkg == sinkPkg && fun.Sel.Name == method
		}
		x, ok := fun.X.(*ast.Ident)
		return ok && pkg != sinkPkg && x.Name == filepath.Base(sinkPkg) && fun.Sel.Name == name
	}
	return false
}

func (s *scan) checkPackage(fset *token.FileSet, imp types.Importer, p listedPackage) error {
	var files []*ast.File
	for _, name := range p.GoFiles {
		f, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		files = append(files, f)
	}
	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	conf := types.Config{Importer: imp}
	if _, err := conf.Check(p.ImportPath, fset, files, info); err != nil {
		return fmt.Errorf("type-check %s: %w", p.ImportPath, err)
	}
	// Errors count everywhere but in l10n, whose own fmt.Errorf renders a
	// message rather than composing one.
	countErrors := p.ImportPath != l10nPkg
	s.pkg = p.ImportPath
	for _, f := range files {
		rel, err := filepath.Rel(s.root, fset.File(f.Pos()).Name())
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		s.files[rel] = true
		for _, decl := range f.Decls {
			fn := ""
			if fd, ok := decl.(*ast.FuncDecl); ok {
				fn = funcName(fd)
				if _, isSink := s.sinks[p.ImportPath+"."+fn]; isSink {
					continue
				}
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				s.checkCall(fset, info, rel, fn, countErrors, call)
				return true
			})
		}
	}
	return nil
}

// funcName names a declaration "Func" or "Recv.Method", the form funcKey gives a
// callee, so a sink's body and its callers resolve to one formatSinks key.
func funcName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	recv := fd.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		recv = star.X
	}
	if index, ok := recv.(*ast.IndexExpr); ok {
		recv = index.X
	}
	if ident, ok := recv.(*ast.Ident); ok {
		return ident.Name + "." + fd.Name.Name
	}
	return fd.Name.Name
}

// callee returns the function or method a call invokes, or nil.
func callee(info *types.Info, call *ast.CallExpr) *types.Func {
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		f, _ := info.Uses[fun].(*types.Func)
		return f
	case *ast.SelectorExpr:
		if sel, ok := info.Selections[fun]; ok {
			f, _ := sel.Obj().(*types.Func)
			return f
		}
		f, _ := info.Uses[fun.Sel].(*types.Func)
		return f
	}
	return nil
}

// funcKey returns "pkg.Func", or "pkg.Recv.Method" for a method.
func funcKey(f *types.Func) string {
	key := f.Pkg().Path() + "."
	if sig, ok := f.Type().(*types.Signature); ok && sig.Recv() != nil {
		recv := sig.Recv().Type()
		if ptr, ok := recv.(*types.Pointer); ok {
			recv = ptr.Elem()
		}
		if named, ok := recv.(*types.Named); ok {
			key += named.Obj().Name() + "."
		}
	}
	return key + f.Name()
}

// constString returns the constant string value of an expression.
func constString(info *types.Info, expr ast.Expr) (string, bool) {
	tv, ok := info.Types[expr]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(tv.Value), true
}

func (s *scan) checkCall(fset *token.FileSet, info *types.Info, file, fn string, countErrors bool, call *ast.CallExpr) {
	f := callee(info, call)
	if f == nil || f.Pkg() == nil {
		return
	}
	add := func(kind, what string) {
		s.findings = append(s.findings, finding{
			file: file, fn: fn, kind: kind, pos: fset.Position(call.Pos()).String(), what: what,
		})
	}
	pkg, name := f.Pkg().Path(), f.Name()
	if index, ok := s.sinks[funcKey(f)]; ok && index < len(call.Args) {
		format, isConst := constString(info, call.Args[index])
		switch {
		case !isConst:
			add(kindSink, name+": the format is not a constant string")
		case !inCatalog(format):
			s.used[format] = true
			add(kindSink, fmt.Sprintf("%s: %q is not in the catalog", name, format))
		default:
			s.used[format] = true
		}
		return
	}
	if pkg == "flag" {
		if name == "NewFlagSet" && s.pkg+"."+fn != s.flagSetMaker {
			s.strayFlagSets = append(s.strayFlagSets, fset.Position(call.Pos()).String())
		}
		if index, ok := flagUsageArg[name]; ok && index < len(call.Args) {
			usage, isConst := constString(info, call.Args[index])
			if isConst {
				s.used[usage] = true
			}
			if !isConst || !inCatalog(usage) {
				add(kindFlag, fmt.Sprintf("flag.%s: description %q is not in the catalog", name, usage))
			}
		}
		return
	}
	if pkg == "fmt" {
		if first, ok := printFuncs[name]; ok {
			for _, arg := range call.Args[min(first, len(call.Args)):] {
				if text, isConst := constString(info, arg); isConst && hasProse(text) {
					add(kindPrint, fmt.Sprintf("fmt.%s writes %q", name, text))
					break
				}
			}
			return
		}
	}
	if countErrors && ((pkg == "fmt" && name == "Errorf") || (pkg == "errors" && name == "New")) {
		add(kindError, pkg+"."+name+" builds an English string, not a message value")
	}
}

func inCatalog(format string) bool {
	_, ok := catalog[format]
	return ok
}

// verbPattern matches one fmt directive, as fmt parses it.
var verbPattern = regexp.MustCompile(`%[-+# 0]*(\[\d+\])?(\*|\d+)?(\.(\[\d+\])?(\*|\d+)?)?(\[\d+\])?[a-zA-Z%]`)

// linePrefixes stay English (docs/CLI.md § Localization); a literal holding only a
// prefix is not prose.
var linePrefixes = []string{"kae: warning:", "kae: note:", "kae:"}

// hasProse reports whether a printed literal holds words: a letter outside the
// fmt directives and the line prefix. "%s\n", " · " and "kae:" are not prose.
func hasProse(text string) bool {
	rest := strings.TrimLeftFunc(text, unicode.IsSpace)
	for _, prefix := range linePrefixes {
		if trimmed, ok := strings.CutPrefix(rest, prefix); ok {
			rest = trimmed
			break
		}
	}
	rest = verbPattern.ReplaceAllString(rest, "")
	return strings.IndexFunc(rest, unicode.IsLetter) >= 0
}

// formatArgs maps each argument a format reads (1-based) to the verbs that read
// it, following fmt's rules for explicit indices and `*` widths.
func formatArgs(format string) map[int][]rune {
	reads := map[int][]rune{}
	arg := 1
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		i++
		if i < len(format) && format[i] == '%' {
			continue
		}
		for i < len(format) && strings.IndexByte("+-# 0", format[i]) >= 0 {
			i++
		}
		explicit := func() {
			if i < len(format) && format[i] == '[' {
				end := strings.IndexByte(format[i:], ']')
				if end > 0 {
					var n int
					if _, err := fmt.Sscanf(format[i+1:i+end], "%d", &n); err == nil {
						arg = n
					}
					i += end + 1
				}
			}
		}
		number := func() {
			explicit()
			if i < len(format) && format[i] == '*' {
				reads[arg] = append(reads[arg], '*')
				arg++
				i++
				return
			}
			for i < len(format) && format[i] >= '0' && format[i] <= '9' {
				i++
			}
		}
		number()
		if i < len(format) && format[i] == '.' {
			i++
			number()
		}
		explicit()
		if i < len(format) {
			r := []rune(format[i:])[0]
			reads[arg] = append(reads[arg], r)
			arg++
		}
	}
	for k := range reads {
		slices.Sort(reads[k])
	}
	return reads
}

// verbsAgree reports whether a Japanese format reads the same arguments with the
// same verbs as its English key; explicit indices may reorder them.
func verbsAgree(english, japanese string) bool {
	en, ja := formatArgs(english), formatArgs(japanese)
	if len(en) != len(ja) {
		return false
	}
	for k, verbs := range en {
		if !slices.Equal(verbs, ja[k]) {
			return false
		}
	}
	return true
}

// ambiguousRunes returns the East Asian Ambiguous characters of s, the class
// displayWidth (internal/cmd/text.go) counts as one column.
func ambiguousRunes(s string) []rune {
	var out []rune
	for _, r := range s {
		if width.LookupRune(r).Kind() == width.EastAsianAmbiguous {
			out = append(out, r)
		}
	}
	return out
}

func TestCatalogAreasDoNotShareKeys(t *testing.T) {
	seen := map[string]string{}
	for areaName, area := range catalogAreas {
		for key := range area {
			if other, dup := seen[key]; dup {
				t.Errorf("%q is in both the %s and the %s area", key, other, areaName)
			}
			seen[key] = areaName
		}
	}
	if len(seen) != len(catalog) {
		t.Errorf("the merged catalog has %d keys, the areas %d", len(catalog), len(seen))
	}
}

func TestCatalogValuesKeepVerbsAndAvoidAmbiguousCharacters(t *testing.T) {
	for key, ja := range catalog {
		if !verbsAgree(key, ja) {
			t.Errorf("format verbs differ:\n  en %q\n  ja %q", key, ja)
		}
		if rs := ambiguousRunes(ja); len(rs) > 0 {
			t.Errorf("%q holds East Asian Ambiguous characters %q (docs/L10N-JA.md § 使える文字)", ja, string(rs))
		}
	}
}

func TestFlagSetsComeFromNewFlagSet(t *testing.T) {
	for _, pos := range scanSource(t).strayFlagSets {
		t.Errorf("%s: flag.NewFlagSet outside %s; call newFlagSet so kae renders the parse failures", pos, flagSetMaker)
	}
}

func TestCatalogHasNoOrphanKeys(t *testing.T) {
	s := scanSource(t)
	for key := range catalog {
		if !s.used[key] {
			t.Errorf("catalog key %q is passed by no sink or flag registration", key)
		}
	}
}

// pendingCounts is one row of the second allowlist.
type pendingCounts struct{ sink, print, flag, error int }

func (c pendingCounts) String() string {
	var parts []string
	for _, p := range []struct {
		name string
		n    int
	}{{"sink", c.sink}, {"print", c.print}, {"flag", c.flag}, {"error", c.error}} {
		if p.n != 0 {
			parts = append(parts, fmt.Sprintf("%s: %d", p.name, p.n))
		}
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func TestHumanSinksUseTheCatalogOrAreAllowlisted(t *testing.T) {
	s := scanSource(t)
	got := map[string]pendingCounts{}
	machineHit := map[string]bool{}
	notLocalizedHit := map[string]bool{}
	var listed []finding
	for _, f := range s.findings {
		if f.kind == kindPrint && machineOutput[f.file+":"+f.fn] {
			machineHit[f.file+":"+f.fn] = true
			continue
		}
		if f.kind == kindError {
			if key, ok := notLocalizedKey(f); ok {
				notLocalizedHit[key] = true
				continue
			}
		}
		c := got[f.file]
		switch f.kind {
		case kindSink:
			c.sink++
		case kindPrint:
			c.print++
		case kindFlag:
			c.flag++
		case kindError:
			c.error++
		}
		got[f.file] = c
		listed = append(listed, f)
	}
	for key := range machineOutput {
		if !machineHit[key] {
			t.Errorf("machine-output allowlist entry %q matches no literal print: remove it", key)
		}
	}
	for key := range notLocalized {
		file, _, _ := strings.Cut(key, ":")
		if !notLocalizedHit[key] && s.files[file] {
			t.Errorf("not-localized allowlist entry %q matches no fmt.Errorf or errors.New: remove it", key)
		}
	}
	files := map[string]bool{}
	for file := range got {
		files[file] = true
	}
	for file := range unmigrated {
		files[file] = true
	}
	var mismatched []string
	for file := range files {
		want, have := unmigrated[file], got[file]
		if want == have {
			continue
		}
		if !s.files[file] {
			if _, err := os.Stat(filepath.Join(s.root, filepath.FromSlash(file))); err == nil {
				continue // in the source, but not in this platform's build
			}
		}
		mismatched = append(mismatched, file)
		if have == (pendingCounts{}) {
			t.Errorf("%s: nothing is left to migrate; remove its allowlist entry %v", file, want)
		} else {
			t.Errorf("%s: the source holds %v unmigrated, the allowlist says %v. A migrated call lowers the entry; "+
				"a new message goes in the catalog rather than raising it", file, have, want)
		}
	}
	if len(mismatched) > 0 {
		slices.Sort(mismatched)
		var b strings.Builder
		for _, f := range listed {
			if slices.Contains(mismatched, f.file) {
				fmt.Fprintf(&b, "  %s [%s] %s: %s\n", f.pos, f.kind, f.fn, f.what)
			}
		}
		t.Logf("findings in the mismatched files:\n%s", b.String())
		t.Logf("the allowlist the source holds now:\n%s", formatAllowlist(got))
	}
}

// notLocalizedKey returns the notLocalized entry an error finding falls under:
// its "file:function" entry, or else its file's.
func notLocalizedKey(f finding) (string, bool) {
	for _, key := range []string{f.file + ":" + f.fn, f.file} {
		if _, ok := notLocalized[key]; ok {
			return key, true
		}
	}
	return "", false
}

func formatAllowlist(got map[string]pendingCounts) string {
	var b strings.Builder
	for _, k := range slices.Sorted(maps.Keys(got)) {
		fmt.Fprintf(&b, "\t%q: %s,\n", k, got[k])
	}
	return b.String()
}
