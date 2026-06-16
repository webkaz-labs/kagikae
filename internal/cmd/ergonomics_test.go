package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

func TestResolveToolArgPrefixes(t *testing.T) {
	// Unambiguous prefixes resolve to the canonical tool id.
	for input, want := range map[string]string{
		"cl":     constants.ToolClaude,
		"cod":    constants.ToolCodex,
		"cu":     constants.ToolCursor,
		"cop":    constants.ToolCopilot,
		"o":      constants.ToolOpencode,
		"a":      constants.ToolAgy,
		"claude": constants.ToolClaude, // exact match wins
	} {
		got, err := resolveToolArg(input)
		if err != nil || got != want {
			t.Fatalf("resolveToolArg(%q) = %q, %v; want %q", input, got, err, want)
		}
	}

	// Ambiguous prefixes error with the candidate list.
	for _, input := range []string{"c", "co"} {
		_, err := resolveToolArg(input)
		if err == nil || exitOf(err) != constants.ExitUsage {
			t.Fatalf("ambiguous prefix %q must be a usage error, got %v", input, err)
		}
	}

	// An unmatched input is returned unchanged (the unknown-tool error fires
	// downstream in validateToolAccount, not here).
	if got, err := resolveToolArg("zz"); err != nil || got != "zz" {
		t.Fatalf("unmatched input must pass through: %q %v", got, err)
	}
}

func TestRunTargetArgs(t *testing.T) {
	// -P is sugar for `all <profile>` and takes no positional.
	if target, name, ok := runTargetArgs("work", nil); !ok || target != "all" || name != "work" {
		t.Fatalf("-P form: %q %q %v", target, name, ok)
	}
	if _, _, ok := runTargetArgs("work", []string{"claude", "x"}); ok {
		t.Fatal("-P with positionals must be rejected")
	}
	// Without -P, exactly two positionals are required.
	if target, name, ok := runTargetArgs("", []string{"claude", "work"}); !ok || target != "claude" || name != "work" {
		t.Fatalf("positional form: %q %q %v", target, name, ok)
	}
	if _, _, ok := runTargetArgs("", []string{"claude"}); ok {
		t.Fatal("one positional must be rejected")
	}
}

func TestCompletionGenerates(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		code, out := captureStdout(t, func() int {
			return CmdCompletion(context.Background(), []string{shell})
		})
		mustExit(t, constants.ExitOK, code, out)
		// The script is dynamic: it delegates to `kae __complete` instead of
		// baking a static word list, so it must reference the backend and the
		// binary, and route the `use` argument position.
		if !strings.Contains(out, "kae __complete") {
			t.Fatalf("%s completion missing the __complete backend:\n%s", shell, out)
		}
		if !strings.Contains(out, "use") {
			t.Fatalf("%s completion missing commands:\n%s", shell, out)
		}
	}

	// Unsupported shell and a missing argument are usage errors.
	if code, _ := captureStdout(t, func() int { return CmdCompletion(context.Background(), []string{"pwsh"}) }); code != constants.ExitUsage {
		t.Fatalf("unsupported shell must exit usage, got %d", code)
	}
	if code, _ := captureStdout(t, func() int { return CmdCompletion(context.Background(), nil) }); code != constants.ExitUsage {
		t.Fatalf("missing shell arg must exit usage, got %d", code)
	}
}
