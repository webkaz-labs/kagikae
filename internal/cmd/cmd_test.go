package cmd

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

func TestRootUnknownCommand(t *testing.T) {
	if got := Root([]string{"missing"}); got != constants.ExitUsage {
		t.Fatalf("expected usage exit, got %d", got)
	}
}

func TestRootVersionAliases(t *testing.T) {
	for _, alias := range [][]string{{"version"}, {"--version"}, {"-v"}} {
		got, output := captureStdout(t, func() int { return Root(alias) })
		if got != constants.ExitOK {
			t.Fatalf("%v: expected ok exit, got %d", alias, got)
		}
		if output != toolName+" "+toolVersion+"\n" {
			t.Fatalf("%v: unexpected output: %q", alias, output)
		}
	}
}

func TestBuildVersionReport(t *testing.T) {
	report := buildVersionReport()
	if report.SchemaVersion != constants.SchemaVersion || report.Tool != toolName {
		t.Fatalf("unexpected: %+v", report)
	}
	if report.Major != 0 || report.Minor != 2 || report.Patch != 0 || report.Contract != "pre_stable" {
		t.Fatalf("unexpected version semantics: %+v", report)
	}
}

func TestRootHelpAliases(t *testing.T) {
	for _, alias := range []string{"help", "--help", "-h"} {
		got, output := captureStdout(t, func() int { return Root([]string{alias}) })
		if got != constants.ExitOK {
			t.Fatalf("expected ok exit for %s, got %d", alias, got)
		}
		if !strings.Contains(output, "kae switch <tool> <account>") {
			t.Fatalf("unexpected help output for %s: %q", alias, output)
		}
	}
}

func TestSplitArgs(t *testing.T) {
	flags, positionals := splitArgs([]string{"all", "work", "--yes", "--format", "json", "--dry-run"})
	if strings.Join(positionals, " ") != "all work" {
		t.Fatalf("positionals: %v", positionals)
	}
	if strings.Join(flags, " ") != "--yes --format json --dry-run" {
		t.Fatalf("flags: %v", flags)
	}
}

func TestSplitArgsValueFlags(t *testing.T) {
	cases := map[string][2]string{
		"--mode":    {"env", "run"},
		"--profile": {"work", "mise"},
		"--to":      {"20260611T000000Z", "rollback"},
		"--format":  {"json", "any"},
		"--config":  {"/tmp/c.toml", "any"},
	}
	for flagName, pair := range cases {
		flags, positionals := splitArgs([]string{flagName, pair[0], "tool", "account"})
		if len(flags) != 2 || flags[1] != pair[0] {
			t.Fatalf("%s: value not kept with flag: flags=%v", flagName, flags)
		}
		if strings.Join(positionals, " ") != "tool account" {
			t.Fatalf("%s: positionals broken: %v", flagName, positionals)
		}
	}
}

func TestParseCommonJSONShorthand(t *testing.T) {
	opts, ok := parseCommon("x", []string{"--json"}, false, nil)
	if !ok || opts.Format != formatJSON {
		t.Fatalf("unexpected: %+v ok=%v", opts, ok)
	}
	_, ok = parseCommon("x", []string{"--format", "yaml"}, false, nil)
	if ok {
		t.Fatal("expected parse failure for unsupported format")
	}
}

func captureStdout(t *testing.T, run func() int) (int, string) {
	t.Helper()
	oldStdout := os.Stdout
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = write
	code := run()
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = oldStdout
	output, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	return code, string(output)
}
