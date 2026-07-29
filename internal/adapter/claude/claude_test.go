package claude

import (
	"fmt"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
)

func TestClaudeFreshnessNestedAndFlat(t *testing.T) {
	exp := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	ms := exp.UnixMilli()
	nested := fmt.Appendf(nil, `{"claudeAiOauth":{"expiresAt":%d,"refreshToken":"r"}}`, ms)
	flat := fmt.Appendf(nil, `{"expiresAt":%d,"refreshToken":"r"}`, ms)
	for name, payload := range map[string][]byte{"nested": nested, "flat": flat} {
		info := Claude{}.Freshness(payload)
		if !info.Known || !info.HasRefresh || !info.ExpiresAt.Equal(exp) {
			t.Fatalf("%s: %+v (want exp %v, refresh true)", name, info, exp)
		}
	}
}

func TestClaudeFreshnessNoRefresh(t *testing.T) {
	info := Claude{}.Freshness([]byte(`{"claudeAiOauth":{"expiresAt":1000000000000}}`))
	if !info.Known || info.HasRefresh || info.ExpiresAt.IsZero() {
		t.Fatalf("Freshness = %+v (want Known, no refresh, dated)", info)
	}
}

// refreshTokenExpiresAt is persisted next to expiresAt, and a Claude Code refresh
// token now lives days rather than a month — so "a refresh token is present"
// stopped implying "recoverable" and the expiry has to be read.
func TestClaudeFreshnessReadsRefreshExpiry(t *testing.T) {
	exp := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	refreshExp := time.Date(2030, 1, 4, 3, 4, 5, 0, time.UTC)
	info := Claude{}.Freshness(fmt.Appendf(nil,
		`{"claudeAiOauth":{"accessToken":"a","refreshToken":"r","expiresAt":%d,"refreshTokenExpiresAt":%d}}`,
		exp.UnixMilli(), refreshExp.UnixMilli()))
	if !info.Known || !info.HasRefresh || !info.RefreshExpiresAt.Equal(refreshExp) {
		t.Fatalf("Freshness = %+v (want refresh expiry %v)", info, refreshExp)
	}
	if info.Invalid {
		t.Fatalf("a populated credential must not be Invalid: %+v", info)
	}
}

// A refresh that fails with invalid_grant makes Claude Code overwrite the
// credential in place with blank tokens and expiresAt 0. Read literally that is
// "no expiry recorded" — the most harmless state kae has — so the adapter must
// translate the tombstone into Invalid.
func TestClaudeFreshnessTombstone(t *testing.T) {
	info := Claude{}.Freshness([]byte(`{"claudeAiOauth":{"accessToken":"","refreshToken":"","expiresAt":0}}`))
	if !info.Known || !info.Invalid || info.HasRefresh || !info.ExpiresAt.IsZero() {
		t.Fatalf("Freshness = %+v (want Known, Invalid, no refresh, zero expiry)", info)
	}
}

// The identity artifact's keys are declared, so a live-vs-applied comparison can
// skip the fields claude renews on a profile refetch. They are exactly the keys
// /login rewrites and a token refresh does not.
func TestClaudeIdentityKeys(t *testing.T) {
	sp := oauthAccountSpec(adapter.Env{Home: "/home/u", Getenv: func(string) string { return "" }})
	if !sp.IdentityOnly {
		t.Fatal("oauth_account must stay identity-only")
	}
	want := map[string]bool{"accountUuid": true, "emailAddress": true, "organizationUuid": true}
	if len(sp.IdentityKeys) != len(want) {
		t.Fatalf("IdentityKeys = %v, want %v", sp.IdentityKeys, want)
	}
	for _, key := range sp.IdentityKeys {
		if !want[key] {
			t.Fatalf("unexpected identity key %q (volatile fields must not be compared)", key)
		}
	}
}

func TestClaudeFreshnessUnparseable(t *testing.T) {
	if info := (Claude{}).Freshness([]byte("not json")); info.Known {
		t.Fatalf("Freshness on garbage = %+v (want Known=false)", info)
	}
}
