package codex

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"

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

// jwt builds a minimal unsigned JWT carrying the given payload JSON.
func jwt(payloadJSON string) string {
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
	writeAuth(t, home, `{"tokens":{"id_token":"`+jwt(`{"email":"bob@example.com"}`)+`","account_id":"acct-123"}}`)
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
