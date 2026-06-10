package cmd

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

func TestRootUnknownCommand(t *testing.T) {
	if got := Root([]string{"missing"}); got != exitUsage {
		t.Fatalf("expected usage exit, got %d", got)
	}
}

func TestBuildCheckReport(t *testing.T) {
	report := buildCheckReport(context.Background(), checkOptions{})
	if report.SchemaVersion != 1 || report.Status != statusOK {
		t.Fatalf("unexpected report: %#v", report)
	}
	if report.Findings == nil || report.Errors == nil {
		t.Fatalf("expected empty slices, got findings=%#v errors=%#v", report.Findings, report.Errors)
	}
}

func TestBuildVersionReport(t *testing.T) {
	report := buildVersionReport()
	if report.SchemaVersion != 1 || report.Tool != toolName || report.Version != toolVersion {
		t.Fatalf("unexpected version report: %#v", report)
	}
	if report.Major != 0 || report.Minor != 1 || report.Patch != 0 || report.Contract != "pre_stable" {
		t.Fatalf("unexpected version semantics: %#v", report)
	}
}

func TestRootVersion(t *testing.T) {
	got, output := captureStdout(t, func() int { return Root([]string{"version"}) })
	if got != exitOK {
		t.Fatalf("expected ok exit, got %d", got)
	}
	if output != toolName+" "+toolVersion+"\n" {
		t.Fatalf("unexpected version output: %q", output)
	}
	got, output = captureStdout(t, func() int { return Root([]string{"--version"}) })
	if got != exitOK {
		t.Fatalf("expected ok exit for --version, got %d", got)
	}
	if output != toolName+" "+toolVersion+"\n" {
		t.Fatalf("unexpected --version output: %q", output)
	}
	for _, alias := range []string{"-v"} {
		got, output = captureStdout(t, func() int { return Root([]string{alias}) })
		if got != exitOK {
			t.Fatalf("expected ok exit for %s, got %d", alias, got)
		}
		if output != toolName+" "+toolVersion+"\n" {
			t.Fatalf("unexpected %s output: %q", alias, output)
		}
	}
}

func TestRootCheckAlias(t *testing.T) {
	if got := Root([]string{"ck", "--no-color"}); got != exitOK {
		t.Fatalf("expected ok exit for ck alias, got %d", got)
	}
}

func TestRootHelpAliases(t *testing.T) {
	for _, alias := range []string{"help", "--help", "-h"} {
		got, output := captureStdout(t, func() int { return Root([]string{alias}) })
		if got != exitOK {
			t.Fatalf("expected ok exit for %s, got %d", alias, got)
		}
		if !strings.Contains(output, "dotfiles-tool help") {
			t.Fatalf("unexpected help output for %s: %q", alias, output)
		}
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

func TestRootAcceptsBareFlags(t *testing.T) {
	if got := Root([]string{"--no-color"}); got != exitOK {
		t.Fatalf("expected ok exit, got %d", got)
	}
}

func TestParseNoColor(t *testing.T) {
	opts, ok := parseCheckOptions([]string{"--no-color"})
	if !ok {
		t.Fatal("expected parse success")
	}
	if !opts.NoColor {
		t.Fatal("expected no-color option")
	}
}
