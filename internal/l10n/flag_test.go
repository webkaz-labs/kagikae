package l10n

import (
	"flag"
	"testing"
)

func TestFlagUsageLooksUpTheDescriptionWithoutFormatting(t *testing.T) {
	translated := &flag.Flag{Name: "x", Usage: "100% of the %s"}
	missing := &flag.Flag{Name: "y", Usage: "not in the catalog"}
	if got := FlagUsage(translated); got != translated.Usage {
		t.Fatalf("English: %q", got)
	}
	withJapanese(t, map[string]string{translated.Usage: "%s の 100%"})
	if got := FlagUsage(translated); got != "%s の 100%" {
		t.Fatalf("Japanese: %q, want the catalog's text unformatted", got)
	}
	if got := FlagUsage(missing); got != missing.Usage {
		t.Fatalf("a description the catalog lacks: %q", got)
	}
}
