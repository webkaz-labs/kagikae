package cmd

import (
	"context"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// resolveAccountName returns the account name for `kae add`: the explicit name
// when given, otherwise the auto-detected, sanitized login identity (the
// v0.8.2 default; docs/RELEASE.md §B). Detection reads the tool's live login
// identity via the adapter.Identifier capability. An adapter without it (agy;
// cursor is discovery-blocked), a detection failure (logged out, unreadable),
// or an identity that sanitizes to empty is a hard error naming the explicit
// form — never a silent fallback.
func (app *App) resolveAccountName(ctx context.Context, tool, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	ad, err := adapter.ForTool(tool)
	if err != nil {
		return "", err
	}
	identifier, ok := ad.(adapter.Identifier)
	if !ok {
		return "", errf(constants.ExitUsage,
			"kae add %s cannot auto-detect an account name; give one: kae add %s <account>", tool, tool)
	}
	raw, err := identifier.Identity(ctx, app.Env)
	if err != nil {
		return "", errf(constants.ExitUsage,
			"could not detect the %s login identity (%v); give an account name: kae add %s <account>",
			tool, err, tool)
	}
	name := sanitizeAccountName(raw)
	if name == "" {
		return "", errf(constants.ExitUsage,
			"the detected %s identity %q has no usable account-name characters; give one: kae add %s <account>",
			tool, raw, tool)
	}
	return name, nil
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
