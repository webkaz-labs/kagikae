package opencode

import (
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

func TestOpencodeIdentityFromAccountID(t *testing.T) {
	home := t.TempDir()
	writeAuth(t, home, `{"openai":{"type":"oauth","accountId":"acct-xyz"},"anthropic":{"type":"api"}}`)
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
