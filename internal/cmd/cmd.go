// Package cmd owns command parsing, report builders, and text/JSON output
// for kae. main.go dispatches here; adapters and IO live below.
package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	// Tool adapters register themselves with internal/adapter.
	_ "github.com/webkaz-labs/kagikae/internal/adapter/agy"
	_ "github.com/webkaz-labs/kagikae/internal/adapter/claude"
	_ "github.com/webkaz-labs/kagikae/internal/adapter/codex"
	_ "github.com/webkaz-labs/kagikae/internal/adapter/copilot"
	_ "github.com/webkaz-labs/kagikae/internal/adapter/cursor"
	_ "github.com/webkaz-labs/kagikae/internal/adapter/opencode"

	// Companion specs register themselves with internal/companion.
	_ "github.com/webkaz-labs/kagikae/internal/companion/cloudflare"
	_ "github.com/webkaz-labs/kagikae/internal/companion/gh"
	_ "github.com/webkaz-labs/kagikae/internal/companion/git"
	_ "github.com/webkaz-labs/kagikae/internal/companion/kubectl"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/l10n"
)

const (
	formatText = "text"
	formatJSON = "json"

	toolName    = "kae"
	toolVersion = "v0.23.0"
)

// Root dispatches the command line.
func Root(args []string) int {
	selectLanguage(args)
	ctx := context.Background()
	if len(args) == 0 {
		return CmdStatus(ctx, nil)
	}
	if args[0] == "--help" || args[0] == "-h" {
		printHelp()
		return constants.ExitOK
	}
	if args[0] == "--version" || args[0] == "-v" {
		return CmdVersion(args[1:])
	}
	if strings.HasPrefix(args[0], "-") {
		return CmdStatus(ctx, args)
	}
	switch args[0] {
	case "__install":
		return CmdInstall(args[1:])
	case "init":
		return CmdInit(ctx, args[1:])
	case "edit":
		return CmdEdit(ctx, args[1:])
	case "doctor", "d":
		return CmdDoctor(ctx, args[1:])
	case "add":
		return CmdAdd(ctx, args[1:])
	case "use", "u":
		return CmdUse(ctx, args[1:])
	case "pin", "p":
		return CmdPin(ctx, args[1:])
	case "unpin":
		return CmdUnpin(ctx, args[1:])
	case "uninstall":
		return CmdUninstall(ctx, args[1:])
	case "relogin":
		return CmdRelogin(ctx, args[1:])
	case "apply":
		return CmdApply(ctx, args[1:])
	case "run", "r":
		return CmdRun(ctx, args[1:])
	case "env":
		return CmdEnv(ctx, args[1:])
	case "companion":
		return CmdCompanion(ctx, args[1:])
	case "mise":
		return CmdMise(ctx, args[1:])
	// Folded into the use/pin × -s/-i surface in v0.7.2;
	// the pointers stay for one release.
	case "bond":
		return removedCommand("bond", "v0.7.2", "kae pin --shared [<profile>]")
	case "as":
		return removedCommand("as", "v0.7.2", "kae pin <tool> <account>")
	// Removed in v0.5.0; the pointers
	// stay for one release. `s` is no longer a switch pointer — it is the
	// status alias (below).
	case "switch":
		return removedCommand(args[0], "v0.5.0", "kae use <profile> | kae use <tool> <account>")
	case "login":
		return removedCommand(args[0], "v0.5.0", "kae add <tool> <account>")
	case "capture":
		return removedCommand(args[0], "v0.5.0", "kae add --no-login <tool> <account>")
	case "current":
		return usageError("kae %s was removed in %s; run: kae (the bare status summary)", args[0], "v0.5.0")
	case "accounts":
		return CmdAccounts(ctx, args[1:])
	case "ls":
		return CmdLs(ctx, args[1:])
	case "open":
		return CmdOpen(ctx, args[1:])
	// The kae shell function (completion.go) handles cd itself; the binary only
	// sees it without the function.
	case "cd":
		return CmdCd(args[1:])
	// Hidden: the path the kae shell function's cd moves to (open.go). Omitted
	// from `kae help` and from completionCommands, like __complete.
	case cdPathCommand:
		return CmdCdPath(ctx, args[1:])
	case "account":
		return CmdAccount(ctx, args[1:])
	case "profile":
		return CmdProfile(ctx, args[1:])
	case "status", "s":
		return CmdStatus(ctx, args[1:])
	case "preservation":
		return CmdPreservation(ctx, args[1:])
	case "backup":
		return CmdBackup(ctx, args[1:])
	case "rollback":
		return CmdRollback(ctx, args[1:])
	case "completion":
		return CmdCompletion(ctx, args[1:])
	// Hidden shell-completion backend (complete.go): omitted from `kae help`
	// and from completionCommands; consumed by generated shell scripts and the
	// mise `complete run="…"` directives.
	case "__complete":
		return CmdComplete(ctx, args[1:])
	// Hidden credential helper invoked by the companion mise exec() template to
	// resolve a token at env-eval time (companion.go). Not in `kae help`.
	case "__companion-token":
		return CmdCompanionToken(ctx, args[1:])
	case "version":
		return CmdVersion(args[1:])
	case "help":
		printHelp()
		return constants.ExitOK
	default:
		return usageError("unknown command: %s (see kae help)%s", args[0], didYouMean(args[0], commandCandidates()))
	}
}

// splitArgs separates flags from positionals so flags may follow
// positionals (kae switch all main --json). The shared value-taking flags
// (--format, --config) are always recognized; commands with their own
// value flags pass the names via valueFlags (e.g. splitArgs(args, "--mode")),
// or their value is misparsed as a positional.
func splitArgs(args []string, valueFlags ...string) (flags, positionals []string) {
	takesValue := map[string]bool{
		"--format": true, "-format": true,
		"--config": true, "-config": true,
	}
	for _, name := range valueFlags {
		base := strings.TrimLeft(name, "-")
		takesValue["--"+base] = true
		takesValue["-"+base] = true
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			positionals = append(positionals, arg)
			continue
		}
		flags = append(flags, arg)
		if takesValue[arg] && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return flags, positionals
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

func CmdVersion(args []string) int {
	flags, positionals := splitArgs(args)
	opts, ok := parseCommon("version", flags, false, nil)
	if !ok {
		return constants.ExitUsage
	}
	if len(positionals) != 0 {
		return usageLine("version [--format text|json]")
	}
	report := buildVersionReport()
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	fmt.Printf("%s %s\n", report.Tool, report.Version)
	return constants.ExitOK
}

func buildVersionReport() versionReport {
	major, minor, patch := parseToolVersion(toolVersion)
	contract := "stable"
	if major == 0 {
		contract = "pre_stable"
	}
	return versionReport{
		SchemaVersion: constants.SchemaVersion,
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

// printHelp writes `kae help` on stdout. Each section is one constant format the
// catalog translates (docs/CLI.md § Localization); the synopsis column and the
// flag names stay verbatim in every language. Each section's format ends in a
// newline, which reportf's own then turns into the blank line before the next. In
// English the sections join into the text testdata/help.en.golden holds.
func printHelp() {
	reportf(helpIntro)
	reportf(helpUsage)
	reportf(helpFlags)
	reportf("Tools: %s", l10n.List(constants.Tools))
}

const helpIntro = `kae - switch AI coding CLI subscription accounts (kagikae)

Two verbs by scope plus run: use = switch now (global), pin = bind this
directory (-s/--shared default, -i/--isolated), run = one process.
`

const helpUsage = `Usage:
  kae [-f|--full]                      status summary: this directory's pin,
                                       global profile, tools, profiles
  kae init                             create config and directories
  kae edit                             open the config in $VISUAL / $EDITOR
  kae doctor [tool] [--json]           environment / auth health checks (alias: kae d)
  kae add <tool> [<account>]           register an account (official login
                                       flow + snapshot; --no-login snapshots
                                       the current login instead)
  kae use [-s|-i] [-P <profile>]       bare: resolve the profile and apply it
                                       idempotently (the former kae apply)
  kae use --auto [-P <profile>]       automatic apply; retain global isolation
                                       (--quiet suppresses success output)
  kae use [-s|-i] <profile>            switch every tool now (alias: kae u)
  kae use <tool> <account>             switch one tool now
  kae pin [-s|-i|-t] [<profile>]       bind this directory (alias: kae p);
                                       -s shares settings/sessions with the real
                                       home (credential private), -i isolates,
                                       -t keeps one store for the directory's
                                       tree across account switches (claude only)
  kae pin <tool> <account>             re-bind one tool inside a bound dir
  kae unpin                            remove the binding from .mise.toml
  kae uninstall [--dry-run] [--yes]    remove owned integrations and a recorded direct binary
  kae relogin [<tool>]                 run the tool's login flow into this
                                       directory's bound store, then capture the
                                       result back into that account's snapshot
  kae run [-s|-i|--env] <t|all> <n> -- C
                                       run C with an account applied (alias: kae r);
                                       -s (default) uses the real home and restores
                                       the previous login after; -i runs in the
                                       per-account isolated home (shared with use -i,
                                       no lock); --env injects env-profile vars only
  kae env set|unset|list ...           env-mode profiles (API keys)
  kae companion add|rm|list ...        bind git/gh/cloud CLI auth to a profile
                                       (per-directory; takes effect via kae pin)
  kae mise init [-P profile] [--auto] [--write]
                                       render the auth-mode tasks + opt-in hook
                                       (bind directories with kae pin instead)
  kae accounts [-f] [--json]           registered accounts
  kae ls [<target>] [-f] [--json]      places and accounts: groups account, pin,
                                       each relevant tool, repo, kae; a target
                                       (account|pin|repo|kae|<tool>) shows one
  kae ls <target> --current|--at N     print one place's path; a tool target
                                       takes -s <tool>, -i <tool> <account>,
                                       --project|--below|--home and --root
  kae open [<target>] [--at N|--pick]  open a place in the file manager (the
                                       current place unless --at or --pick; same
                                       targets and selectors as kae ls, no
                                       account; several places open the picker)
  kae cd [<target>] [--at N|--pick]    move the shell to a place; needs the kae
                                       shell function that eval "$(kae
                                       completion zsh)" (or bash) defines
  kae status [-f] [--json]             full status report (alias: kae s);
                                       -f/--full on status, accounts and ls adds
                                       the Identity and Driver columns
  kae preservation list [--json]       list preserved credential records
  kae preservation restore <id>        restore to the original credential store
  kae preservation rm <id>             delete a preserved record with confirmation
  kae backup list [--json]             list switch backups
  kae rollback [--to <backup-id>]      restore a backup
  kae completion <bash|zsh|fish>       print a shell completion script and the
                                       kae shell function (--no-function: the
                                       completion alone, for a completion file)
  kae version | --version | -v
  kae help | --help | -h
`

const helpFlags = `Flags (structured commands):
  --json                shorthand for --format json
  --format text|json    output format
  --dry-run             preview without writing (add --no-login/use/rollback)
  --yes                 non-interactive confirmation (reserved)
  --no-color            disable color
  --config <path>       explicit config file path
`

// removedCommand reports a removed or renamed command and names its
// replacement (kept for one release). replacement is inserted verbatim in every
// language, so it holds only a command line; a replacement that needs explaining
// gets its own constant format instead (see "current" in Root and CmdApply).
func removedCommand(old, version, replacement string) int {
	return usageError("kae %s was removed in %s; run: %s", old, version, replacement)
}
