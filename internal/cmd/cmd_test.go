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
	if report.Major != 0 || report.Minor != 11 || report.Patch != 0 || report.Contract != "pre_stable" {
		t.Fatalf("unexpected version semantics: %+v", report)
	}
}

func TestRootHelpAliases(t *testing.T) {
	for _, alias := range []string{"help", "--help", "-h"} {
		got, output := captureStdout(t, func() int { return Root([]string{alias}) })
		if got != constants.ExitOK {
			t.Fatalf("expected ok exit for %s, got %d", alias, got)
		}
		if !strings.Contains(output, "kae use <tool> <account>") {
			t.Fatalf("unexpected help output for %s: %q", alias, output)
		}
	}
}

func TestSplitArgs(t *testing.T) {
	flags, positionals := splitArgs([]string{"all", "main", "--yes", "--format", "json", "--dry-run"})
	if strings.Join(positionals, " ") != "all main" {
		t.Fatalf("positionals: %v", positionals)
	}
	if strings.Join(flags, " ") != "--yes --format json --dry-run" {
		t.Fatalf("flags: %v", flags)
	}
}

func TestSplitArgsValueFlags(t *testing.T) {
	// shared value flags are always recognized
	for _, flagName := range []string{"--format", "--config"} {
		flags, positionals := splitArgs([]string{flagName, "value", "tool", "account"})
		if len(flags) != 2 || flags[1] != "value" {
			t.Fatalf("%s: value not kept with flag: flags=%v", flagName, flags)
		}
		if strings.Join(positionals, " ") != "tool account" {
			t.Fatalf("%s: positionals broken: %v", flagName, positionals)
		}
	}
	// command-specific value flags are passed by the call site (both dash forms)
	for _, form := range []string{"--mode", "-mode"} {
		flags, positionals := splitArgs([]string{form, "env", "tool", "account"}, "--mode")
		if len(flags) != 2 || flags[1] != "env" {
			t.Fatalf("%s: value not kept with flag: flags=%v", form, flags)
		}
		if strings.Join(positionals, " ") != "tool account" {
			t.Fatalf("%s: positionals broken: %v", form, positionals)
		}
	}
	// without registration the value is (correctly) a positional
	flags, positionals := splitArgs([]string{"--mode", "env"})
	if len(flags) != 1 || len(positionals) != 1 {
		t.Fatalf("unregistered flag must not consume a value: %v %v", flags, positionals)
	}
	// kae add registers --identity as a value flag (regression: its value was
	// misclassified as the tool/account positional).
	flags, positionals = splitArgs([]string{"--no-login", "--identity", "you@example.com", "agy", "kazsky"}, "--identity")
	if strings.Join(positionals, " ") != "agy kazsky" {
		t.Fatalf("--identity value leaked into positionals: %v", positionals)
	}
	if strings.Join(flags, " ") != "--no-login --identity you@example.com" {
		t.Fatalf("--identity value not kept with flag: %v", flags)
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
