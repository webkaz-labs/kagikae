package codex

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
)

func testEnv(home string) adapter.Env {
	return adapter.Env{
		GOOS:     "linux",
		Home:     home,
		Getenv:   func(string) string { return "" },
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
	}
}

// makeJWT builds a minimal unsigned JWT carrying the given payload JSON.
func makeJWT(payloadJSON string) string {
	seg := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	return seg(`{"alg":"none"}`) + "." + seg(payloadJSON) + "."
}

func writeAuth(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCodexIdentityFromIDTokenEmail(t *testing.T) {
	home := t.TempDir()
	writeAuth(t, home, `{"tokens":{"id_token":"`+makeJWT(`{"email":"bob@example.com"}`)+`","account_id":"acct-123"}}`)
	got, err := Codex{}.Identity(t.Context(), testEnv(home))
	if err != nil || got != "bob@example.com" {
		t.Fatalf("Identity = %q, err = %v; want bob@example.com", got, err)
	}
}

func TestCodexIdentityFallsBackToAccountID(t *testing.T) {
	home := t.TempDir()
	writeAuth(t, home, `{"tokens":{"id_token":"not-a-jwt","account_id":"acct-123"}}`)
	got, err := Codex{}.Identity(t.Context(), testEnv(home))
	if err != nil || got != "acct-123" {
		t.Fatalf("Identity = %q, err = %v; want acct-123", got, err)
	}
}

func TestCodexIdentityMissing(t *testing.T) {
	home := t.TempDir()
	writeAuth(t, home, `{"tokens":{}}`)
	var c Codex
	if _, err := c.Identity(t.Context(), testEnv(home)); err == nil {
		t.Fatal("expected an error when no email claim or account_id is present")
	}
}

// makeJWTExp builds an unsigned JWT carrying an exp claim (seconds).
func makeJWTExp(exp int64) string { return makeJWT(fmt.Sprintf(`{"exp":%d}`, exp)) }

func TestCodexFreshnessJWTExpiryAndRefresh(t *testing.T) {
	exp := time.Date(2031, 6, 1, 0, 0, 0, 0, time.UTC)
	payload := fmt.Appendf(nil, `{"tokens":{"access_token":%q,"refresh_token":"r"}}`, makeJWTExp(exp.Unix()))
	info := Codex{}.Freshness(payload)
	if !info.Known || !info.HasRefresh || !info.ExpiresAt.Equal(exp) {
		t.Fatalf("Freshness = %+v (want exp %v, refresh true)", info, exp)
	}
}

func TestCodexFreshnessAPIKeyOnly(t *testing.T) {
	info := Codex{}.Freshness([]byte(`{"OPENAI_API_KEY":"sk-x"}`))
	if !info.Known || info.HasRefresh || !info.ExpiresAt.IsZero() {
		t.Fatalf("Freshness = %+v (want Known, no refresh, no expiry)", info)
	}
}

func TestCodexFreshnessUnparseable(t *testing.T) {
	if info := (Codex{}).Freshness([]byte("not json")); info.Known {
		t.Fatalf("Freshness on garbage = %+v (want Known=false)", info)
	}
}
