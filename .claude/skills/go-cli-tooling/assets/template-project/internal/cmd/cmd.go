package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 64

	formatText = "text"
	formatJSON = "json"

	statusOK = "ok"

	toolName    = "dotfiles-tool"
	toolVersion = "v0.1.0"
)

type checkOptions struct {
	Format  string
	NoColor bool
}

type checkReport struct {
	SchemaVersion int    `json:"schema_version"`
	Status        string `json:"status"`
	Message       string `json:"message"`
	Findings      []item `json:"findings"`
	Errors        []item `json:"errors"`
}

type versionReport struct {
	SchemaVersion int    `json:"schema_version"`
	Tool          string `json:"tool"`
	Version       string `json:"version"`
	Major         int    `json:"major"`
	Minor         int    `json:"minor"`
	Patch         int    `json:"patch"`
	Contract      string `json:"contract"`
}

type item struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func Root(args []string) int {
	ctx := context.Background()
	if len(args) == 0 {
		return CmdCheck(ctx, nil)
	}
	if isHelpAlias(args[0]) {
		printHelp()
		return exitOK
	}
	if isVersionAlias(args[0]) {
		return CmdVersion(args[1:])
	}
	if strings.HasPrefix(args[0], "-") {
		return CmdCheck(ctx, args)
	}
	switch args[0] {
	case "check", "ck":
		return CmdCheck(ctx, args[1:])
	case "version":
		return CmdVersion(args[1:])
	case "help":
		printHelp()
		return exitOK
	default:
		return usageError("unknown command: %s", args[0])
	}
}

func isVersionAlias(arg string) bool {
	return arg == "--version" || arg == "-v"
}

func isHelpAlias(arg string) bool {
	return arg == "--help" || arg == "-h"
}

func CmdVersion(args []string) int {
	format := formatText
	if len(args) > 0 {
		if len(args) == 2 && args[0] == "--format" {
			format = args[1]
		} else {
			return usageError("usage: %s version [--format text|json]", toolName)
		}
	}
	if format != formatText && format != formatJSON {
		return usageError("unsupported format: %s", format)
	}
	report := buildVersionReport()
	if format == formatJSON {
		return encodeJSON(report)
	}
	fmt.Printf("%s %s\n", report.Tool, report.Version)
	return exitOK
}

func CmdCheck(ctx context.Context, args []string) int {
	opts, ok := parseCheckOptions(args)
	if !ok {
		return exitUsage
	}
	report := buildCheckReport(ctx, opts)
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	fmt.Printf("%s: %s\n", report.Status, report.Message)
	return exitOK
}

func parseCheckOptions(args []string) (checkOptions, bool) {
	var opts checkOptions
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&opts.Format, "format", formatText, "output format: text or json")
	fs.BoolVar(&opts.NoColor, "no-color", false, "disable color in human text output")
	if err := fs.Parse(args); err != nil {
		return opts, false
	}
	if opts.Format != formatText && opts.Format != formatJSON {
		usageError("unsupported format: %s", opts.Format)
		return opts, false
	}
	return opts, true
}

func buildCheckReport(_ context.Context, _ checkOptions) checkReport {
	return checkReport{
		SchemaVersion: 1,
		Status:        statusOK,
		Message:       "no checks implemented yet",
		Findings:      []item{},
		Errors:        []item{},
	}
}

func buildVersionReport() versionReport {
	major, minor, patch := parseToolVersion(toolVersion)
	contract := "stable"
	if major == 0 {
		contract = "pre_stable"
	}
	return versionReport{
		SchemaVersion: 1,
		Tool:          toolName,
		Version:       toolVersion,
		Major:         major,
		Minor:         minor,
		Patch:         patch,
		Contract:      contract,
	}
}

func parseToolVersion(version string) (int, int, int) {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return 0, 0, 0
	}
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	patch, _ := strconv.Atoi(parts[2])
	return major, minor, patch
}

func printHelp() {
	fmt.Println(`dotfiles-tool

Usage:
  dotfiles-tool [--format text|json] [--no-color]
  dotfiles-tool check|ck [--format text|json] [--no-color]
  dotfiles-tool version [--format text|json]
  dotfiles-tool --version | -v
  dotfiles-tool help | --help | -h`)
}
