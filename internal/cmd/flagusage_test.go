package cmd

import (
	"bytes"
	"flag"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// flagSpecUnderTest is one command's flag set as parseCommon builds it.
type flagSpecUnderTest struct {
	name   string
	dryRun bool
	extra  func(*flag.FlagSet)
}

// allFlagSpecs is every command in commandFlagSpecs, the common set alone and the
// hidden installer.
func allFlagSpecs() []flagSpecUnderTest {
	specs := []flagSpecUnderTest{
		{name: "version"},
		{name: "__install", extra: func(fs *flag.FlagSet) { registerInstallFlags(fs, new(string), new(string), new(string)) }},
	}
	for _, name := range slices.Sorted(maps.Keys(commandFlagSpecs)) {
		spec := commandFlagSpecs[name]
		specs = append(specs, flagSpecUnderTest{name: name, dryRun: spec.dryRun, extra: spec.extra})
	}
	return specs
}

// stdlibParse is what the flag package itself prints for args: a real FlagSet
// with its default output and usage.
func stdlibParse(spec flagSpecUnderTest, args []string) (bool, string) {
	var out bytes.Buffer
	fs := flag.NewFlagSet(spec.name, flag.ContinueOnError)
	fs.SetOutput(&out)
	registerCommonFlags(fs, new(commonOpts), spec.dryRun)
	if spec.extra != nil {
		spec.extra(fs)
	}
	return fs.Parse(args) == nil, out.String()
}

// argvCases are the command lines of every failure kind for spec's flags: help,
// an unknown flag, each valued flag without its argument, each bool flag given a
// bad value, bad syntax (rendered verbatim) and a parse that stops at `--`.
func argvCases(spec flagSpecUnderTest) [][]string {
	cases := [][]string{
		{"-h"},
		{"--help"},
		{"-help=1"},
		{"--bogus"},
		{"-bogus=1"},
		{"--json", "--bogus"},
		{"---x"},
		{"-=x"},
		{"--", "--bogus"},
	}
	fs := newFlagSet(spec.name)
	registerCommonFlags(fs, new(commonOpts), spec.dryRun)
	if spec.extra != nil {
		spec.extra(fs)
	}
	fs.VisitAll(func(f *flag.Flag) {
		if flagTakesValue(f) {
			cases = append(cases, []string{"--" + f.Name}, []string{"--yes", "-" + f.Name})
		} else {
			cases = append(cases, []string{"--" + f.Name + "=maybe"})
		}
	})
	return cases
}

// TestParseFailuresPrintWhatTheFlagPackagePrints compares kae's English rendering
// of every parse failure and usage block with the flag package's own output,
// byte for byte, for every command's flag set.
func TestParseFailuresPrintWhatTheFlagPackagePrints(t *testing.T) {
	extra := map[string][][]string{
		"ls":        {{"--at", "x"}, {"--at=0"}, {"-at", "-1"}},
		"open":      {{"--at", "x"}},
		"cd":        {{"--at=x"}},
		"uninstall": {{"--dir", ""}, {"--dir", "a\nb"}, {"--dir=ok", "--dir="}},
		"use":       {{"-s=maybe"}, {"-P"}},
	}
	for _, spec := range allFlagSpecs() {
		for _, args := range append(argvCases(spec), extra[spec.name]...) {
			wantOK, want := stdlibParse(spec, args)
			var gotOK bool
			_, got := captureStderr(t, func() int {
				_, gotOK = parseCommon(spec.name, args, spec.dryRun, spec.extra)
				return 0
			})
			if gotOK != wantOK || got != want {
				t.Errorf("%s %q: ok %v, stderr\n%s\nflag package: ok %v, stderr\n%s", spec.name, args, gotOK, got, wantOK, want)
			}
		}
	}
}

// TestParseFailureKindsAreRenderedPerKind pins the first line of each kind, so a
// kind that falls back to the verbatim error would show here even if the flag
// package's text matched.
func TestParseFailureKindsAreRenderedPerKind(t *testing.T) {
	l10ntest.UseJapanese(t)
	ls := flagSpecUnderTest{name: "ls", extra: commandFlagSpecs["ls"].extra}
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--bogus"}, "定義されていないフラグです: -bogus\n"},
		{[]string{"--format"}, "フラグに値が指定されていません: -format\n"},
		{[]string{"--at", "x"}, `フラグ -at の値 "x" は無効です: --at には 1 以上の場所の番号を指定してください（指定値: "x"）` + "\n"},
		{[]string{"--json=maybe"}, `フラグ -json の真偽値 "maybe" は無効です: parse error` + "\n"},
		{[]string{"---x"}, "bad flag syntax: ---x\n"},
		{[]string{"-h"}, "ls の使い方:\n"},
	}
	for _, c := range cases {
		_, got := captureStderr(t, func() int {
			parseCommon(ls.name, c.args, false, ls.extra)
			return 0
		})
		first, _, _ := strings.Cut(got, "\n")
		if first+"\n" != c.want {
			t.Errorf("%q: first line %q, want %q", c.args, first, c.want)
		}
		if c.args[0] != "-h" && !strings.Contains(got, "\nls の使い方:\n") {
			t.Errorf("%q: the usage block must follow the error line:\n%s", c.args, got)
		}
		if !strings.Contains(got, "\n  -format string\n    \t出力形式: text または json（既定値: \"text\"）\n") {
			t.Errorf("%q: the usage block must localize its default suffix:\n%s", c.args, got)
		}
	}
}

// TestFlagValueErrorsAreLocalizedAndEnglishInJSONMode runs the two Values whose Set
// fails with kae's own message through Root: Japanese for a person, English when
// the command line is in JSON mode.
func TestFlagValueErrorsAreLocalizedAndEnglishInJSONMode(t *testing.T) {
	l10ntest.UseJapanese(t)
	cases := []struct {
		args   []string
		ja, en string
	}{
		{
			[]string{"ls", "--at", "x"},
			`フラグ -at の値 "x" は無効です: --at には 1 以上の場所の番号を指定してください（指定値: "x"）`,
			`invalid value "x" for flag -at: --at takes a place number from 1, got "x"`,
		},
		{
			[]string{"uninstall", "--dir", ""},
			`フラグ -dir の値 "" は無効です: ディレクトリには空でない 1 行のパスを指定してください`,
			`invalid value "" for flag -dir: directory must be a nonempty single-line path`,
		},
	}
	for _, c := range cases {
		code, stderr := captureStderr(t, func() int { return Root(c.args) })
		if first, _, _ := strings.Cut(stderr, "\n"); code != constants.ExitUsage || first != c.ja {
			t.Errorf("Root(%q): exit %d, first line %q, want %q", c.args, code, first, c.ja)
		}
		jsonArgs := append(slices.Clone(c.args[:1]), append([]string{"--json"}, c.args[1:]...)...)
		code, stderr = captureStderr(t, func() int { return Root(jsonArgs) })
		if first, _, _ := strings.Cut(stderr, "\n"); code != constants.ExitUsage || first != c.en {
			t.Errorf("Root(%q): exit %d, first line %q, want %q", jsonArgs, code, first, c.en)
		}
		if !strings.Contains(stderr, "\nUsage of "+c.args[0]+":\n") {
			t.Errorf("Root(%q): the usage block must stay English:\n%s", jsonArgs, stderr)
		}
	}
}

// TestUsageBlockPrintsTheCatalogsFlagDescriptions runs `kae use -h` through Root:
// the descriptions are the catalog's Japanese for a person, a one-letter flag
// keeping its description on the same line, and English in JSON mode.
func TestUsageBlockPrintsTheCatalogsFlagDescriptions(t *testing.T) {
	l10ntest.UseJapanese(t)
	code, stderr := captureStderr(t, func() int { return Root([]string{"use", "-h"}) })
	if code != constants.ExitUsage {
		t.Errorf("use -h: exit %d, want %d", code, constants.ExitUsage)
	}
	for _, want := range []string{
		"use の使い方:\n",
		"\n  -P string\n    \t--profile の別名\n",
		"\n  -s\t--shared の別名\n",
		"\n  -auto\n    \tグローバルな独立環境の選択を保ったまま、解決したプロファイルを適用\n",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("use -h: the usage block lacks %q:\n%s", want, stderr)
		}
	}
	_, stderr = captureStderr(t, func() int { return Root([]string{"use", "--json", "-h"}) })
	if !strings.Contains(stderr, "\n  -P string\n    \talias for --profile\n") {
		t.Errorf("use --json -h: the descriptions must stay English:\n%s", stderr)
	}
}
