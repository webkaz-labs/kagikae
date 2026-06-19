package cmd

import (
	"context"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// resolveAccount returns the account name and the raw detected login identity
// for `kae add`. The name is the explicit one when given, otherwise the
// auto-detected, sanitized login identity (the v0.8.2 default; docs/RELEASE.md
// §B). The raw identity is returned for the snapshot's identity field (§D),
// best-effort even when an explicit name is given ("" when undetectable).
//
// Detection reads the tool's live login identity via the adapter.Identifier
// capability. With no explicit name, an adapter without it (agy), a detection
// failure (logged out, unreadable), or an identity that sanitizes to empty is a
// hard error naming the explicit form — never a silent fallback. With an
// explicit name, detection never blocks the capture: the name wins and the
// identity is recorded only if it happened to be readable.
func (app *App) resolveAccount(ctx context.Context, tool, explicit string) (name, identity string, err error) {
	ad, err := adapter.ForTool(tool)
	if err != nil {
		return "", "", err
	}
	identifier, hasIdentifier := ad.(adapter.Identifier)
	if explicit != "" {
		if hasIdentifier {
			// Best-effort: a detection failure must not block an explicit name.
			if raw, derr := identifier.Identity(ctx, app.Env); derr == nil {
				identity = strings.TrimSpace(raw)
			}
		}
		return explicit, identity, nil
	}
	if !hasIdentifier {
		return "", "", errf(constants.ExitUsage,
			"kae add %s cannot auto-detect an account name; give one: kae add %s <account>", tool, tool)
	}
	raw, derr := identifier.Identity(ctx, app.Env)
	if derr != nil {
		return "", "", errf(constants.ExitUsage,
			"could not detect the %s login identity (%v); give an account name: kae add %s <account>",
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
