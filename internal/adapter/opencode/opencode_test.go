package opencode

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

// makeJWT builds a minimal unsigned JWT carrying the given payload JSON.
func makeJWT(payloadJSON string) string {
	seg := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	return seg(`{"alg":"none"}`) + "." + seg(payloadJSON) + "."
}

func testEnv(home string) adapter.Env {
	return adapter.Env{
		GOOS:     "linux",
		Home:     home,
		Getenv:   func(string) string { return "" },
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
	}
}

func writeAuth(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".local", "share", "opencode")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The access token's OpenAI profile email is preferred over the opaque
// accountId UUID, so the auto-detected name is human-readable.
func TestOpencodeIdentityPrefersProfileEmail(t *testing.T) {
	home := t.TempDir()
	access := makeJWT(`{"https://api.openai.com/profile":{"email":"work@example.com"}}`)
	writeAuth(t, home, `{"openai":{"type":"oauth","access":"`+access+`","accountId":"39a1e863-uuid"}}`)
	got, err := Opencode{}.Identity(t.Context(), testEnv(home))
	if err != nil || got != "work@example.com" {
		t.Fatalf("Identity = %q, err = %v; want work@example.com (not the accountId)", got, err)
	}
}

// With no email claim in the access token, Identity falls back to accountId.
func TestOpencodeIdentityFallsBackToAccountID(t *testing.T) {
	home := t.TempDir()
	writeAuth(t, home, `{"openai":{"type":"oauth","access":"not-a-jwt","accountId":"acct-xyz"},"anthropic":{"type":"api"}}`)
	got, err := Opencode{}.Identity(t.Context(), testEnv(home))
	if err != nil || got != "acct-xyz" {
		t.Fatalf("Identity = %q, err = %v; want acct-xyz", got, err)
	}
}

func TestOpencodeIdentityMissing(t *testing.T) {
	home := t.TempDir()
	writeAuth(t, home, `{"anthropic":{"type":"api"}}`)
	var o Opencode
	if _, err := o.Identity(t.Context(), testEnv(home)); err == nil {
		t.Fatal("expected an error when openai.accountId is absent")
	}
}

func TestOpencodeFreshnessExpiresMs(t *testing.T) {
	exp := time.Date(2029, 3, 3, 12, 0, 0, 0, time.UTC)
	payload := fmt.Appendf(nil, `{"type":"oauth","refresh":"r","access":"a","expires":%d}`, exp.UnixMilli())
	info := Opencode{}.Freshness(payload)
	if !info.Known || !info.HasRefresh || !info.ExpiresAt.Equal(exp) {
		t.Fatalf("Freshness = %+v (want exp %v, refresh true)", info, exp)
	}
}

func TestOpencodeFreshnessUnparseable(t *testing.T) {
	if info := (Opencode{}).Freshness([]byte("not json")); info.Known {
		t.Fatalf("Freshness on garbage = %+v (want Known=false)", info)
	}
}
