package cmd

import (
	"context"
	"fmt"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/companion"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// CmdComplete is the hidden shell-completion backend:
//
//	kae __complete <kind> [args]
//
// It prints one candidate per line from kae's live surface and is the single
// source every completion path consults (kae's own shell completion in
// completion.go and the mise task `complete run="…"` directives from
// miseinit.go), so candidate lists never drift from the real router/config/
// state. It is read-only, takes no locks, and is intentionally omitted from
// `kae help` and from completionCommands (it is not a public command).
//
// The line-oriented output is an internal contract consumed by generated shell
// scripts, not the JSON contract (schema_version is unaffected). A config error
// is non-fatal — completion still works with an empty profile list — so it
// falls back to defaults rather than refusing.
//
// Kinds:
//   - commands         — the router's public commands (completionCommands)
//   - tools            — constants.Tools
//   - companions       — constants.Companions (companion ids: git, gh, …)
//   - companion-knobs <id> — the named companion's knob names (its Spec)
//   - profiles         — config profile names
//   - accounts [<tool>]— captured account names, optionally scoped to one tool
//   - flags <command>  — a command's flags (--name / -n), from the same
//     registrars the parser uses (flagspec.go), so the list never drifts
func CmdComplete(_ context.Context, args []string) int {
	// commands and tools are compile-time constants, so the most frequent
	// completion (word 1 → commands) skips newApp's config load entirely.
	if len(args) > 0 {
		switch args[0] {
		case "commands":
			printCompletionLines(completionCommands)
			return constants.ExitOK
		case "tools":
			printCompletionLines(constants.Tools)
			return constants.ExitOK
		case "companions":
			// Companion ids are compile-time constants, like tools.
			printCompletionLines(constants.Companions)
			return constants.ExitOK
		case "companion-knobs":
			// A companion's knob names come from its registered Spec (static), so
			// this skips newApp's config load too.
			id := ""
			if len(args) > 1 {
				id = args[1]
			}
			printCompletionLines(companionKnobNames(id))
			return constants.ExitOK
		case "flags":
			// A command's flags are compile-time, so flag completion (the current
			// word starts with -) skips newApp's config load like commands/tools.
			cmd := ""
			if len(args) > 1 {
				cmd = args[1]
			}
			printCompletionLines(flagCompletions(cmd))
			return constants.ExitOK
		}
	}
	return runComplete(newApp(""), args)
}

// runComplete is the testable core of CmdComplete (App injected so tests use a
// temp-HOME app instead of the live environment). It handles the kinds that
// read live state; CmdComplete short-circuits the constant kinds before it.
func runComplete(app *App, args []string) int {
	if len(args) == 0 {
		return constants.ExitUsage
	}
	switch args[0] {
	case "commands":
		printCompletionLines(completionCommands)
	case "tools":
		printCompletionLines(constants.Tools)
	case "companions":
		printCompletionLines(constants.Companions)
	case "companion-knobs":
		id := ""
		if len(args) > 1 {
			id = args[1]
		}
		printCompletionLines(companionKnobNames(id))
	case "flags":
		cmd := ""
		if len(args) > 1 {
			cmd = args[1]
		}
		printCompletionLines(flagCompletions(cmd))
	case "profiles":
		printCompletionLines(app.Config.ProfileNames())
	case "accounts":
		tool := ""
		if len(args) > 1 {
			tool = args[1]
		}
		names, err := completionAccountNames(app, tool)
		if err != nil {
			return constants.ExitError
		}
		printCompletionLines(names)
	default:
		return constants.ExitUsage
	}
	return constants.ExitOK
}

// completionAccountNames returns captured account names for completion. With a
// tool given (a canonical id or a prefix alias), it scopes to that tool; with
// none, it returns every captured account name, deduplicated across tools. The
// order follows account.List (canonical tool order then name).
func completionAccountNames(app *App, tool string) ([]string, error) {
	if tool != "" {
		// Best-effort: resolve a prefix alias to the canonical id; an
		// ambiguous or unknown input is left as-is and simply matches nothing.
		if resolved, err := resolveToolArg(tool); err == nil {
			tool = resolved
		}
		// Scoped read: only that tool's dir, names already unique per tool.
		accounts, err := account.ListForTool(app.Paths.AccountsDir(), tool)
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(accounts))
		for _, acc := range accounts {
			names = append(names, acc.Name)
		}
		return names, nil
	}
	// No tool: every captured account name, deduplicated across tools.
	accounts, err := account.List(app.Paths.AccountsDir())
	if err != nil {
		return nil, err
	}
	names := []string{}
	seen := map[string]bool{}
	for _, acc := range accounts {
		if seen[acc.Name] {
			continue
		}
		seen[acc.Name] = true
		names = append(names, acc.Name)
	}
	return names, nil
}

// companionKnobNames returns the knob names of the named companion for
// completion (e.g. git → email/name/signingkey), in Spec order. An unknown id
// yields nothing, so `kae companion add <profile> <bogus> <TAB>` matches nothing
// rather than erroring.
func companionKnobNames(id string) []string {
	spec, ok := companion.For(id)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(spec.Knobs))
	for _, k := range spec.Knobs {
		names = append(names, k.Name)
	}
	return names
}

// printCompletionLines writes one candidate per line to stdout (the internal
// completion contract). An empty slice prints nothing.
func printCompletionLines(lines []string) {
	for _, line := range lines {
		fmt.Println(line)
	}
}
