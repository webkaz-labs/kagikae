package cursor

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
)

func cursorEnv() adapter.Env {
	return adapter.Env{GOOS: "darwin", Getenv: func(string) string { return "" }}
}

func TestCursorIdentityLoggedIn(t *testing.T) {
	fake := &runnertest.Fake{Stdout: "✓ Logged in as you@example.com\n", Code: 0}
	var got string
	var err error
	runner.With(fake, func() { got, err = Cursor{}.Identity(context.Background(), cursorEnv()) })
	if err != nil || got != "you@example.com" {
		t.Fatalf("Identity = %q, err = %v; want you@example.com", got, err)
	}
	if fake.Name != "cursor-agent" || len(fake.Args) != 1 || fake.Args[0] != "status" {
		t.Fatalf("ran %q %v; want cursor-agent status", fake.Name, fake.Args)
	}
}

func TestCursorIdentityFailures(t *testing.T) {
	cases := map[string]*runnertest.Fake{
		"logged out (exit 1)": {Stderr: "not logged in", Code: 1},
		"no marker":           {Stdout: "Status: unknown\n", Code: 0},
		"empty identity":      {Stdout: "✓ Logged in as \n", Code: 0},
	}
	for name, fake := range cases {
		t.Run(name, func(t *testing.T) {
			var err error
			runner.With(fake, func() { _, err = Cursor{}.Identity(context.Background(), cursorEnv()) })
			if err == nil {
				t.Fatalf("%s: expected a detection error", name)
			}
		})
	}
}

// makeJWT builds a minimal unsigned-looking JWT whose payload carries exp.
func makeJWT(exp int64) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, `{"exp":%d}`, exp))
	return header + "." + payload + ".sig"
}

func TestCursorFreshnessOpaqueJWT(t *testing.T) {
	exp := time.Date(2028, 9, 9, 9, 9, 9, 0, time.UTC)
	info := Cursor{}.Freshness([]byte(makeJWT(exp.Unix())))
	if !info.Known || info.HasRefresh || !info.ExpiresAt.Equal(exp) {
		t.Fatalf("Freshness = %+v (want exp %v, no refresh)", info, exp)
	}
}

func TestCursorFreshnessNonJWT(t *testing.T) {
	if info := (Cursor{}).Freshness([]byte("not-a-jwt")); info.Known {
		t.Fatalf("Freshness on non-JWT = %+v (want Known=false)", info)
	}
}
