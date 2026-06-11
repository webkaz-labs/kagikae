package artifact

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/runner"
)

func TestFileKindRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	target := filepath.Join(dir, "codex", "auth.json")
	sp := Spec{Name: "auth", Kind: constants.KindFile, Target: target}

	v, err := ReadLive(ctx, sp)
	if err != nil || v.Present {
		t.Fatalf("expected absent: %+v %v", v, err)
	}
	if err := ApplyLive(ctx, sp, Value{Data: []byte(`{"t":"x"}`), Present: true}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode: %v", info.Mode())
	}
	v, err = ReadLive(ctx, sp)
	if err != nil || !v.Present || string(v.Data) != `{"t":"x"}` {
		t.Fatalf("read back: %+v %v", v, err)
	}
	// applying an absent value removes the live file
	if err := ApplyLive(ctx, sp, Value{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("file should be removed")
	}
	if err := ApplyLive(ctx, sp, Value{}); err != nil {
		t.Fatal("removing an absent artifact must be safe:", err)
	}
}

func TestJSONPointerKindPreservesSiblings(t *testing.T) {
	ctx := context.Background()
	target := filepath.Join(t.TempDir(), ".claude.json")
	doc := `{"oauthAccount":{"accountUuid":"old"},"projects":{"/r":{}},"n":12345678901234567890}`
	if err := os.WriteFile(target, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	sp := Spec{Name: "oauth_account", Kind: constants.KindJSONPointer, Target: target, Pointer: "/oauthAccount"}

	v, err := ReadLive(ctx, sp)
	if err != nil || !v.Present {
		t.Fatalf("read: %+v %v", v, err)
	}
	if err := ApplyLive(ctx, sp, Value{Data: []byte(`{"accountUuid":"new"}`), Present: true}); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(target)
	var parsed map[string]any
	dec := json.NewDecoder(strings.NewReader(string(out)))
	dec.UseNumber()
	if err := dec.Decode(&parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["oauthAccount"].(map[string]any)["accountUuid"] != "new" {
		t.Fatalf("not patched: %s", out)
	}
	if _, ok := parsed["projects"]; !ok {
		t.Fatalf("sibling lost: %s", out)
	}
	if parsed["n"].(json.Number).String() != "12345678901234567890" {
		t.Fatalf("number corrupted: %s", out)
	}
}

func TestJSONPointerKindMissingFileCreates(t *testing.T) {
	ctx := context.Background()
	target := filepath.Join(t.TempDir(), "sub", ".credentials.json")
	sp := Spec{Name: "oauth", Kind: constants.KindJSONPointer, Target: target, Pointer: "/claudeAiOauth"}
	if err := ApplyLive(ctx, sp, Value{Data: []byte(`{"accessToken":"x"}`), Present: true}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode: %v", info.Mode())
	}
}

func TestJSONPointerKindRefusesGarbage(t *testing.T) {
	ctx := context.Background()
	target := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(target, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	sp := Spec{Name: "x", Kind: constants.KindJSONPointer, Target: target, Pointer: "/a"}
	if _, err := ReadLive(ctx, sp); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("read should refuse: %v", err)
	}
	err := ApplyLive(ctx, sp, Value{Data: []byte(`1`), Present: true})
	if !errors.Is(err, ErrUnsafe) {
		t.Fatalf("apply should refuse: %v", err)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "not json" {
		t.Fatal("refused write must not modify the file")
	}
}

type fakeRunner struct {
	payloads map[string]string // keyed by service
	writes   []string
	accounts map[string]string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) (string, string, int) {
	if name != "security" {
		return "", "unexpected command " + name, 1
	}
	switch args[0] {
	case "find-generic-password":
		service := args[2]
		payload, ok := f.payloads[service]
		if !ok {
			return "", "security: ... could not be found ...", 44
		}
		if args[len(args)-1] == "-w" {
			return payload + "\n", "", 0
		}
		acct := f.accounts[service]
		return `    "acct"<blob>="` + acct + `"` + "\n", "", 0
	case "add-generic-password":
		var service, account, payload string
		for i := 0; i+1 < len(args); i++ {
			switch args[i] {
			case "-s":
				service = args[i+1]
			case "-a":
				account = args[i+1]
			case "-w":
				payload = args[i+1]
			}
		}
		f.payloads[service] = payload
		f.accounts[service] = account
		f.writes = append(f.writes, service)
		return "", "", 0
	case "delete-generic-password":
		service := args[2]
		if _, ok := f.payloads[service]; !ok {
			return "", "security: ... could not be found ...", 44
		}
		delete(f.payloads, service)
		delete(f.accounts, service)
		f.writes = append(f.writes, service)
		return "", "", 0
	}
	return "", "unexpected args", 1
}

func (f *fakeRunner) RunInput(ctx context.Context, _ string, name string, args ...string) (string, string, int) {
	return f.Run(ctx, name, args...)
}

func TestKeychainKindRoundTrip(t *testing.T) {
	ctx := context.Background()
	const liveItem = `{"claudeAiOauth":{"accessToken":"old"}}`
	fake := &fakeRunner{
		payloads: map[string]string{"Claude Code-credentials": liveItem},
		accounts: map[string]string{"Claude Code-credentials": "realuser"},
	}
	sp := Spec{
		Name: "claude_ai_oauth", Kind: constants.KindKeychain,
		Target: "Claude Code-credentials", Pointer: "/claudeAiOauth",
		KeychainAccount: "fallback",
	}
	const newItem = `{"claudeAiOauth":{"accessToken":"new"}}`
	runner.With(fake, func() {
		v, err := ReadLive(ctx, sp)
		if err != nil || !v.Present {
			t.Fatalf("read: %+v %v", v, err)
		}
		// ReadLive stores the whole item verbatim, not an extracted sub-value.
		if string(v.Data) != liveItem {
			t.Fatalf("item not captured verbatim: %s", v.Data)
		}
		if err := ApplyLive(ctx, sp, Value{Data: []byte(newItem), Present: true}); err != nil {
			t.Fatal(err)
		}
	})
	if got := fake.payloads["Claude Code-credentials"]; got != newItem {
		t.Fatalf("payload not written verbatim: %s", got)
	}
	if fake.accounts["Claude Code-credentials"] != "realuser" {
		t.Fatalf("existing account not reused: %s", fake.accounts["Claude Code-credentials"])
	}
}

// TestKeychainKindWritesVerbatim guards the core fix: the captured bytes are
// written exactly as-is. Claude Code stores compact, unsorted JSON and rejects
// a re-serialized payload, so kagikae must not pretty-print or sort keys.
func TestKeychainKindWritesVerbatim(t *testing.T) {
	ctx := context.Background()
	// Deliberately compact and NOT key-sorted ("z" before "a").
	const item = `{"claudeAiOauth":{"z":1,"accessToken":"t"}}`
	fake := &fakeRunner{payloads: map[string]string{}, accounts: map[string]string{}}
	sp := Spec{
		Name: "claude_ai_oauth", Kind: constants.KindKeychain,
		Target: "Claude Code-credentials", Pointer: "/claudeAiOauth",
		KeychainAccount: "u",
	}
	runner.With(fake, func() {
		if err := ApplyLive(ctx, sp, Value{Data: []byte(item), Present: true}); err != nil {
			t.Fatal(err)
		}
	})
	if got := fake.payloads["Claude Code-credentials"]; got != item {
		t.Fatalf("payload was re-serialized (formatting/key order changed):\n got: %s\nwant: %s", got, item)
	}
}

// TestKeychainKindAbsentDeletesItem: applying an absent value removes the
// live item (mirrors the file/json-pointer absent cases).
func TestKeychainKindAbsentDeletesItem(t *testing.T) {
	ctx := context.Background()
	fake := &fakeRunner{
		payloads: map[string]string{"Claude Code-credentials": `{"claudeAiOauth":{}}`},
		accounts: map[string]string{"Claude Code-credentials": "u"},
	}
	sp := Spec{Name: "x", Kind: constants.KindKeychain, Target: "Claude Code-credentials", Pointer: "/claudeAiOauth"}
	runner.With(fake, func() {
		if err := ApplyLive(ctx, sp, Value{Present: false}); err != nil {
			t.Fatal(err)
		}
	})
	if _, ok := fake.payloads["Claude Code-credentials"]; ok {
		t.Fatal("absent apply must delete the keychain item")
	}
}

func TestKeychainKindRefusesUnknownPayload(t *testing.T) {
	ctx := context.Background()
	fake := &fakeRunner{
		payloads: map[string]string{"Claude Code-credentials": "opaque-blob"},
		accounts: map[string]string{},
	}
	sp := Spec{Name: "x", Kind: constants.KindKeychain, Target: "Claude Code-credentials", Pointer: "/claudeAiOauth"}
	runner.With(fake, func() {
		if _, err := ReadLive(ctx, sp); !errors.Is(err, ErrUnsafe) {
			t.Fatalf("read should refuse: %v", err)
		}
		err := ApplyLive(ctx, sp, Value{Data: []byte(`{}`), Present: true})
		if !errors.Is(err, ErrUnsafe) {
			t.Fatalf("apply should refuse: %v", err)
		}
	})
	if len(fake.writes) != 0 {
		t.Fatal("refused write must not touch the keychain")
	}
}

func TestKeychainKindCreatesWithFallbackAccount(t *testing.T) {
	ctx := context.Background()
	fake := &fakeRunner{payloads: map[string]string{}, accounts: map[string]string{}}
	sp := Spec{
		Name: "x", Kind: constants.KindKeychain,
		Target: "Claude Code-credentials", Pointer: "/claudeAiOauth",
		KeychainAccount: "fallbackuser",
	}
	runner.With(fake, func() {
		if err := ApplyLive(ctx, sp, Value{Data: []byte(`{"claudeAiOauth":{"a":1}}`), Present: true}); err != nil {
			t.Fatal(err)
		}
	})
	if fake.accounts["Claude Code-credentials"] != "fallbackuser" {
		t.Fatalf("fallback account not used: %q", fake.accounts["Claude Code-credentials"])
	}
}
