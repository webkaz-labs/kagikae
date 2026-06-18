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

// TestJSONPointerKindJSONCRoundTrip: a JSONC spec reads through comments and
// writes the pointer value back while preserving the leading // comments and
// sibling keys (GitHub Copilot's config.json shape).
func TestJSONPointerKindJSONCRoundTrip(t *testing.T) {
	ctx := context.Background()
	target := filepath.Join(t.TempDir(), "config.json")
	doc := "// managed automatically\n{\n  \"trustedFolders\": [\"/w\"],\n  \"lastLoggedInUser\": {\"host\":\"h\",\"login\":\"a\"}\n}\n"
	if err := os.WriteFile(target, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	sp := Spec{Name: "last_logged_in_user", Kind: constants.KindJSONPointer,
		Target: target, Pointer: "/lastLoggedInUser", JSONC: true}

	v, err := ReadLive(ctx, sp)
	if err != nil || !v.Present {
		t.Fatalf("read through comments: %+v %v", v, err)
	}
	if err := ApplyLive(ctx, sp, Value{Data: []byte(`{"host":"h","login":"b"}`), Present: true}); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(target)
	s := string(out)
	if !strings.Contains(s, "// managed automatically") {
		t.Fatalf("comment lost: %s", s)
	}
	if !strings.Contains(s, `"login":"b"`) && !strings.Contains(s, `"login": "b"`) {
		t.Fatalf("value not switched: %s", s)
	}
	if !strings.Contains(s, "trustedFolders") {
		t.Fatalf("sibling lost: %s", s)
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

// TestKeychainReplaceUsesCapturedAccount covers the codex-keyring path: a
// KeychainReplace spec writes under its captured opaque account (not the live
// item's) and deletes the prior item first, so exactly one item remains.
func TestKeychainReplaceUsesCapturedAccount(t *testing.T) {
	ctx := context.Background()
	fake := &fakeRunner{
		payloads: map[string]string{"Codex Auth": `{"tokens":{"access_token":"live-other"}}`},
		accounts: map[string]string{"Codex Auth": "cli|opaqueLIVE"},
	}
	sp := Spec{
		Name: "auth", Kind: constants.KindKeychain, Target: "Codex Auth",
		Pointer: "/tokens", KeychainAccount: "cli|opaqueTARGET", KeychainReplace: true,
	}
	const target = `{"tokens":{"access_token":"target"}}`
	runner.With(fake, func() {
		if err := ApplyLive(ctx, sp, Value{Data: []byte(target), Present: true}); err != nil {
			t.Fatal(err)
		}
	})
	if fake.accounts["Codex Auth"] != "cli|opaqueTARGET" {
		t.Fatalf("account = %q, want the captured cli|opaqueTARGET", fake.accounts["Codex Auth"])
	}
	if fake.payloads["Codex Auth"] != target {
		t.Fatalf("payload = %q, want %q", fake.payloads["Codex Auth"], target)
	}
	// The prior item must have been deleted before the write (a delete then an add).
	if len(fake.writes) != 2 || fake.writes[0] != "Codex Auth" || fake.writes[1] != "Codex Auth" {
		t.Fatalf("expected delete-then-add, got writes %v", fake.writes)
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

// TestKeychainKindOpaquePayload covers a non-JSON keychain item (an empty
// pointer marks it opaque, as Cursor stores a bare JWT): the raw bytes
// round-trip verbatim with no JSON structure guard.
func TestKeychainKindOpaquePayload(t *testing.T) {
	ctx := context.Background()
	const liveToken = "eyJhbGciOiJIUzI1NiJ9.payload.sig"
	fake := &fakeRunner{
		payloads: map[string]string{"cursor-access-token": liveToken},
		accounts: map[string]string{"cursor-access-token": "cursor-user"},
	}
	sp := Spec{
		Name: "access_token", Kind: constants.KindKeychain,
		Target: "cursor-access-token", Pointer: "", KeychainAccount: "cursor-user",
	}
	const newToken = "eyJhbGciOiJIUzI1NiJ9.other.sig2"
	runner.With(fake, func() {
		v, err := ReadLive(ctx, sp)
		if err != nil || !v.Present || string(v.Data) != liveToken {
			t.Fatalf("opaque read: %+v %v", v, err)
		}
		if err := ApplyLive(ctx, sp, Value{Data: []byte(newToken), Present: true}); err != nil {
			t.Fatal(err)
		}
	})
	if got := fake.payloads["cursor-access-token"]; got != newToken {
		t.Fatalf("opaque payload not written verbatim: %s", got)
	}
	if fake.accounts["cursor-access-token"] != "cursor-user" {
		t.Fatalf("existing account not reused: %s", fake.accounts["cursor-access-token"])
	}
}

// TestKeychainKindOpaqueRefusesEmpty: an opaque spec still refuses an empty
// payload rather than writing a blank credential.
func TestKeychainKindOpaqueRefusesEmpty(t *testing.T) {
	ctx := context.Background()
	fake := &fakeRunner{payloads: map[string]string{}, accounts: map[string]string{}}
	sp := Spec{Name: "x", Kind: constants.KindKeychain, Target: "cursor-access-token", Pointer: ""}
	runner.With(fake, func() {
		if err := ApplyLive(ctx, sp, Value{Data: []byte(""), Present: true}); !errors.Is(err, ErrUnsafe) {
			t.Fatalf("empty opaque payload must be refused: %v", err)
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

// acctFakeRunner is a security double keyed by service+account, so it can prove
// a match-account spec never touches a sibling item of a shared service.
type acctFakeRunner struct {
	items  map[string]string // key: service "\x00" account
	writes []string          // mutation log: "add:<key>" / "delete:<key>"
}

func acctKey(service, account string) string { return service + "\x00" + account }

func flagVal(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

func (f *acctFakeRunner) Run(_ context.Context, name string, args ...string) (string, string, int) {
	if name != "security" {
		return "", "unexpected command " + name, 1
	}
	service, account := flagVal(args, "-s"), flagVal(args, "-a")
	switch args[0] {
	case "find-generic-password":
		// match-account reads always pass -a; refuse a service-only probe here so
		// the test fails loudly if scoping regresses.
		if account == "" {
			return "", "test: match-account read must pass -a", 1
		}
		payload, ok := f.items[acctKey(service, account)]
		if !ok {
			return "", "security: ... could not be found ...", 44
		}
		if args[len(args)-1] == "-w" {
			return payload + "\n", "", 0
		}
		return `    "acct"<blob>="` + account + `"` + "\n", "", 0
	case "add-generic-password":
		key := acctKey(service, account)
		f.items[key] = flagVal(args, "-w")
		f.writes = append(f.writes, "add:"+key)
		return "", "", 0
	case "delete-generic-password":
		key := acctKey(service, account)
		if _, ok := f.items[key]; !ok {
			return "", "security: ... could not be found ...", 44
		}
		delete(f.items, key)
		f.writes = append(f.writes, "delete:"+key)
		return "", "", 0
	}
	return "", "unexpected args", 1
}

func (f *acctFakeRunner) RunInput(ctx context.Context, _ string, name string, args ...string) (string, string, int) {
	return f.Run(ctx, name, args...)
}

// TestKeychainMatchAccountScopesToAccount covers agy's gemini/antigravity item:
// read, write, and delete touch only the antigravity account, leaving a sibling
// gemini item (a different account) untouched. No replace-delete, no
// reuse-existing-account from the shared service.
func TestKeychainMatchAccountScopesToAccount(t *testing.T) {
	ctx := context.Background()
	sibling := acctKey("gemini", "someone-else") // a non-agy gemini item
	fake := &acctFakeRunner{items: map[string]string{
		acctKey("gemini", "antigravity"): "agy-live-token",
		sibling:                          "gemini-cli-token",
	}}
	sp := Spec{
		Name: "credential", Kind: constants.KindKeychain, Target: "gemini",
		Pointer: "", KeychainAccount: "antigravity", KeychainMatchAccount: true,
	}
	runner.With(fake, func() {
		v, err := ReadLive(ctx, sp)
		if err != nil || !v.Present || string(v.Data) != "agy-live-token" {
			t.Fatalf("scoped read: %+v %v", v, err)
		}
		if err := ApplyLive(ctx, sp, Value{Data: []byte("agy-new-token"), Present: true}); err != nil {
			t.Fatal(err)
		}
	})
	if fake.items[acctKey("gemini", "antigravity")] != "agy-new-token" {
		t.Fatalf("antigravity item not written verbatim: %q", fake.items[acctKey("gemini", "antigravity")])
	}
	if fake.items[sibling] != "gemini-cli-token" {
		t.Fatalf("sibling gemini item must survive untouched: %q", fake.items[sibling])
	}
	// Apply must be a single add (no delete-replace that could remove a sibling).
	for _, w := range fake.writes {
		if strings.HasPrefix(w, "delete:") {
			t.Fatalf("match-account apply must not delete: %v", fake.writes)
		}
	}
}

// An absent match-account value deletes only the antigravity item, never the
// sibling.
func TestKeychainMatchAccountAbsentDeletesOnlyOwnItem(t *testing.T) {
	ctx := context.Background()
	sibling := acctKey("gemini", "someone-else")
	fake := &acctFakeRunner{items: map[string]string{
		acctKey("gemini", "antigravity"): "agy-live-token",
		sibling:                          "gemini-cli-token",
	}}
	sp := Spec{
		Name: "credential", Kind: constants.KindKeychain, Target: "gemini",
		Pointer: "", KeychainAccount: "antigravity", KeychainMatchAccount: true,
	}
	runner.With(fake, func() {
		if err := ApplyLive(ctx, sp, Value{Present: false}); err != nil {
			t.Fatal(err)
		}
	})
	if _, ok := fake.items[acctKey("gemini", "antigravity")]; ok {
		t.Fatal("antigravity item must be deleted")
	}
	if fake.items[sibling] != "gemini-cli-token" {
		t.Fatalf("sibling gemini item must survive: %q", fake.items[sibling])
	}
}

// A multi-line opaque payload is refused (agy's structure guard is non-empty,
// single-line): an interior newline signals corruption.
func TestKeychainOpaqueRefusesMultiline(t *testing.T) {
	sp := Spec{Name: "x", Kind: constants.KindKeychain, Target: "gemini", Pointer: ""}
	if err := keychainGuard(sp, []byte("line1\nline2")); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("multi-line opaque payload must be refused: %v", err)
	}
	if err := keychainGuard(sp, []byte("single-line-token")); err != nil {
		t.Fatalf("single-line opaque payload must pass: %v", err)
	}
}
