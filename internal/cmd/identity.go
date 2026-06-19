package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// resolveAccount returns the account name and the login identity for `kae add`.
// The name is the explicit one when given, otherwise derived (sanitized) from
// the identity (the v0.8.2 default; docs/RELEASE.md §B). The identity is
// recorded in the snapshot's identity field (§D).
//
// identityOverride is the `--identity` value: when set it is the identity
// verbatim (sanitized), so an account whose login identity is not readable on
// disk — e.g. agy on current Antigravity, where the live account lives behind an
// opaque keychain token and is never written to disk — can still record one.
//
// Otherwise the identity comes from the adapter.Identifier capability. With no
// explicit name and no override, an adapter without it (agy), a detection
// failure, or an identity that sanitizes to empty is a hard error naming the
// explicit form — never a silent fallback. With an explicit name and no
// override, detection never blocks the capture: the name wins, and a detection
// failure is warned (not silent) so a missing identity is visible.
func (app *App) resolveAccount(ctx context.Context, tool, explicit, identityOverride string) (name, identity string, err error) {
	ad, err := adapter.ForTool(tool)
	if err != nil {
		return "", "", err
	}
	override := sanitizeIdentity(identityOverride)
	identifier, hasIdentifier := ad.(adapter.Identifier)
	if explicit != "" {
		switch {
		case override != "":
			identity = override
		case hasIdentifier:
			if raw, derr := identifier.Identity(ctx, app.Env); derr == nil {
				identity = strings.TrimSpace(raw)
			} else {
				// Not an error: identity is optional metadata, and some tools
				// cannot expose it to kae (agy on current Antigravity resolves the
				// account from an opaque keychain token, never written to disk).
				// Frame it as a calm, optional note — not a failure — and point at
				// the explicit fix. The raw cause is intentionally omitted so a
				// missing file does not read like a bug.
				fmt.Fprintf(os.Stderr,
					"kae: note: no login identity could be detected for %s; %s/%s was captured without one (identity is optional). Add it anytime: kae account set-identity %s %s <value>\n",
					tool, tool, explicit, tool, explicit)
			}
		}
		return explicit, identity, nil
	}
	if override != "" {
		// No explicit name: derive it from the supplied identity, same rule as a
		// detected one.
		name = sanitizeAccountName(override)
		if name == "" {
			return "", "", errf(constants.ExitUsage,
				"--identity %q has no usable account-name characters; give a name: kae add %s <account>",
				identityOverride, tool)
		}
		return name, override, nil
	}
	if !hasIdentifier {
		return "", "", errf(constants.ExitUsage,
			"kae add %s cannot auto-detect an account name; give one: kae add %s <account> (or pass --identity <value>)", tool, tool)
	}
	raw, derr := identifier.Identity(ctx, app.Env)
	if derr != nil {
		return "", "", errf(constants.ExitUsage,
			"could not detect the %s login identity (%v); give an account name: kae add %s <account> (or pass --identity <value>)",
			tool, derr, tool)
	}
	name = sanitizeAccountName(raw)
	if name == "" {
		return "", "", errf(constants.ExitUsage,
			"the detected %s identity %q has no usable account-name characters; give one: kae add %s <account>",
			tool, raw, tool)
	}
	return name, strings.TrimSpace(raw), nil
}

// sanitizeIdentity normalizes a supplied or detected login identity for storage
// and display: it trims surrounding space and drops control characters (so the
// value cannot break --json output or inject terminal escapes), then caps the
// length at 256 runes. It does not enforce an email shape — some tools'
// identity is a bare handle (copilot). Returns "" when nothing usable remains.
func sanitizeIdentity(raw string) string {
	raw = strings.TrimSpace(raw)
	var b strings.Builder
	var n int
	for _, r := range raw {
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
		n++
		if n >= 256 {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

// sanitizeAccountName turns a raw login identity into a valid account name: an
// email keeps only its local part (before @), then characters outside
// [a-zA-Z0-9._-] are dropped and the result is capped at 64. The output passes
// config.ValidName (or is empty, which the caller rejects). Never logs or
// stores the raw identity beyond the sanitized name.
func sanitizeAccountName(raw string) string {
	raw = strings.TrimSpace(raw)
	if at := strings.IndexByte(raw, '@'); at >= 0 {
		raw = raw[:at]
	}
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		}
		if b.Len() >= 64 {
			break
		}
	}
	return b.String()
}
