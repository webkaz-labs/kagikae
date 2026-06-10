package patch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mixedDoc = `{
  "oauthAccount": {"accountUuid": "old", "emailAddress": "a@example.com"},
  "projects": {"/repo": {"allowedTools": []}},
  "mcpServers": {"x": {"command": "x"}},
  "bigNumber": 12345678901234567890,
  "float": 1.5,
  "firstStartTime": "2024-01-01T00:00:00Z"
}`

func TestGetPointer(t *testing.T) {
	raw, ok, err := GetPointer([]byte(mixedDoc), "/oauthAccount")
	if err != nil || !ok {
		t.Fatalf("GetPointer failed: ok=%v err=%v", ok, err)
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if v["accountUuid"] != "old" {
		t.Fatalf("unexpected value: %v", v)
	}
}

func TestGetPointerMissing(t *testing.T) {
	_, ok, err := GetPointer([]byte(mixedDoc), "/missing")
	if err != nil || ok {
		t.Fatalf("expected missing, got ok=%v err=%v", ok, err)
	}
	_, ok, err = GetPointer([]byte(mixedDoc), "/oauthAccount/missing/deep")
	if err != nil || ok {
		t.Fatalf("expected missing deep, got ok=%v err=%v", ok, err)
	}
}

func TestSetPointerPreservesSiblings(t *testing.T) {
	out, err := SetPointer([]byte(mixedDoc), "/oauthAccount", json.RawMessage(`{"accountUuid":"new"}`))
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	dec := json.NewDecoder(strings.NewReader(string(out)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	if v["oauthAccount"].(map[string]any)["accountUuid"] != "new" {
		t.Fatalf("pointer not replaced: %s", out)
	}
	for _, key := range []string{"projects", "mcpServers", "firstStartTime", "float"} {
		if _, ok := v[key]; !ok {
			t.Fatalf("sibling %s lost: %s", key, out)
		}
	}
	if got := v["bigNumber"].(json.Number).String(); got != "12345678901234567890" {
		t.Fatalf("big number corrupted: %s", got)
	}
	if !strings.Contains(string(out), "12345678901234567890") {
		t.Fatalf("big number not preserved literally: %s", out)
	}
}

func TestSetPointerCreatesMissingKey(t *testing.T) {
	out, err := SetPointer([]byte(`{"keep": 1}`), "/oauthAccount", json.RawMessage(`"x"`))
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatal(err)
	}
	if v["oauthAccount"] != "x" || v["keep"] == nil {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestDeletePointer(t *testing.T) {
	out, err := DeletePointer([]byte(mixedDoc), "/oauthAccount")
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatal(err)
	}
	if _, ok := v["oauthAccount"]; ok {
		t.Fatal("key not deleted")
	}
	if _, ok := v["projects"]; !ok {
		t.Fatal("sibling lost")
	}
	// deleting a missing key is not an error
	if _, err := DeletePointer([]byte(mixedDoc), "/nope"); err != nil {
		t.Fatal(err)
	}
}

func TestSetPointerRejectsNonObjectRoot(t *testing.T) {
	if _, err := SetPointer([]byte(`[1]`), "/a", json.RawMessage(`1`)); err == nil {
		t.Fatal("expected error for array root")
	}
}

func TestInvalidPointer(t *testing.T) {
	if _, _, err := GetPointer([]byte(`{}`), "noSlash"); err == nil {
		t.Fatal("expected invalid pointer error")
	}
}

func TestWriteFileAtomicPreservesMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cred.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode not preserved: %v", info.Mode())
	}
	data, _ := os.ReadFile(path)
	if string(data) != "new" {
		t.Fatalf("content not written: %s", data)
	}
}

func TestWriteFileAtomicNewFileMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.json")
	if err := WriteFileAtomic(path, []byte("x"), CredentialFileMode); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("unexpected mode: %v", info.Mode())
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, ".*tmp*"))
	if len(leftovers) != 0 {
		t.Fatalf("temp files left: %v", leftovers)
	}
}
