package keychain

import (
	"context"
	"encoding/hex"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
)

func TestReadItemPlain(t *testing.T) {
	fake := &runnertest.Fake{Stdout: `{"claudeAiOauth":{"a":1}}` + "\n"}
	runner.With(fake, func() {
		payload, found, err := ReadItem(context.Background(), "Claude Code-credentials")
		if err != nil || !found || string(payload) != `{"claudeAiOauth":{"a":1}}` {
			t.Fatalf("unexpected: %s %v %v", payload, found, err)
		}
	})
}

func TestReadItemHexEncoded(t *testing.T) {
	payload := `{"claudeAiOauth":{"name":"日本語"}}`
	fake := &runnertest.Fake{Stdout: hex.EncodeToString([]byte(payload)) + "\n"}
	runner.With(fake, func() {
		got, found, err := ReadItem(context.Background(), "svc")
		if err != nil || !found || string(got) != payload {
			t.Fatalf("hex decode failed: %s %v %v", got, found, err)
		}
	})
}

func TestReadItemNotFound(t *testing.T) {
	fake := &runnertest.Fake{Stderr: "security: ... could not be found ...", Code: 44}
	runner.With(fake, func() {
		_, found, err := ReadItem(context.Background(), "svc")
		if err != nil || found {
			t.Fatalf("expected not found: %v %v", found, err)
		}
	})
}

func TestItemAccountParsesAcct(t *testing.T) {
	fake := &runnertest.Fake{Stdout: "keychain: \"login\"\nattributes:\n    \"acct\"<blob>=\"alice\"\n"}
	runner.With(fake, func() {
		acct, found, err := ItemAccount(context.Background(), "svc")
		if err != nil || !found || acct != "alice" {
			t.Fatalf("unexpected: %q %v %v", acct, found, err)
		}
	})
}

func argsContain(args []string, want ...string) bool {
	for i := 0; i+len(want) <= len(args); i++ {
		match := true
		for j, w := range want {
			if args[i+j] != w {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// ReadItemForAccount / DeleteItemForAccount scope the security call to a single
// account of a shared service (agy's gemini/antigravity) by passing -a, so a
// sibling item under a different account is never read or removed.
func TestReadItemForAccountScopesByAccount(t *testing.T) {
	fake := &runnertest.Fake{Stdout: "opaque-token\n"}
	runner.With(fake, func() {
		payload, found, err := ReadItemForAccount(context.Background(), "gemini", "antigravity")
		if err != nil || !found || string(payload) != "opaque-token" {
			t.Fatalf("unexpected: %s %v %v", payload, found, err)
		}
		if !argsContain(fake.Args, "-s", "gemini") || !argsContain(fake.Args, "-a", "antigravity") {
			t.Fatalf("find must be scoped by service and account: %v", fake.Args)
		}
	})
}

func TestDeleteItemForAccountScopesByAccount(t *testing.T) {
	fake := &runnertest.Fake{}
	runner.With(fake, func() {
		if err := DeleteItemForAccount(context.Background(), "gemini", "antigravity"); err != nil {
			t.Fatal(err)
		}
		if fake.Args[0] != "delete-generic-password" ||
			!argsContain(fake.Args, "-s", "gemini") || !argsContain(fake.Args, "-a", "antigravity") {
			t.Fatalf("delete must be scoped by service and account: %v", fake.Args)
		}
	})
}

// The service-only ReadItem must not pass -a, so the single-item drivers
// (claude/cursor/codex) keep their account-agnostic match.
func TestReadItemServiceOnlyOmitsAccount(t *testing.T) {
	fake := &runnertest.Fake{Stdout: "{}\n"}
	runner.With(fake, func() {
		if _, _, err := ReadItem(context.Background(), "svc"); err != nil {
			t.Fatal(err)
		}
		if argsContain(fake.Args, "-a") {
			t.Fatalf("service-only read must not pass -a: %v", fake.Args)
		}
	})
}

func TestDecodeHexPayloadRejectsPlainHexPassword(t *testing.T) {
	// a password that merely looks hexadecimal must stay literal
	if _, ok := decodeHexPayload("deadbeef"); ok {
		t.Fatal("non-JSON hex must not be decoded")
	}
}
