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

func TestDecodeHexPayloadRejectsPlainHexPassword(t *testing.T) {
	// a password that merely looks hexadecimal must stay literal
	if _, ok := decodeHexPayload("deadbeef"); ok {
		t.Fatal("non-JSON hex must not be decoded")
	}
}
