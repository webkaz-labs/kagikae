package patch

import (
	"bytes"
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

func TestStrictJSONRejectsTrailingInputBeforeAnyOperation(t *testing.T) {
	documents := []string{
		`{"a":1} {"b":2}`,
		`{"a":1} trailing`,
		"{\"a\":1 // comment\n}",
		`{"a":1,}`,
	}
	for _, doc := range documents {
		t.Run(doc, func(t *testing.T) {
			if _, _, err := GetPointer([]byte(doc), "/a"); err == nil {
				t.Fatal("GetPointer accepted invalid strict JSON")
			}
			if _, err := SetPointer([]byte(doc), "/a", json.RawMessage(`2`)); err == nil {
				t.Fatal("SetPointer accepted invalid strict JSON")
			}
			if _, err := DeletePointer([]byte(doc), "/missing"); err == nil {
				t.Fatal("DeletePointer accepted invalid strict JSON on a no-op path")
			}
		})
	}
}

func TestStrictJSONRejectsDuplicateMembersAtAnyDepth(t *testing.T) {
	documents := []string{
		`{"a":1,"a":2}`,
		`{"outer":{"a":1,"a":2}}`,
	}
	for _, doc := range documents {
		t.Run(doc, func(t *testing.T) {
			if _, _, err := GetPointer([]byte(doc), "/a"); err == nil {
				t.Fatal("GetPointer accepted duplicate object members")
			}
			if _, err := SetPointer([]byte(doc), "/x", json.RawMessage(`1`)); err == nil {
				t.Fatal("SetPointer accepted duplicate object members")
			}
			if _, err := DeletePointer([]byte(doc), "/missing"); err == nil {
				t.Fatal("DeletePointer accepted duplicate object members on a no-op path")
			}
		})
	}
}

func TestSetPointerRejectsInvalidStrictJSONValue(t *testing.T) {
	values := []string{
		`1 2`,
		`{"a":1,"a":2}`,
		`{"outer":{"a":1,"a":2}}`,
		"{\"a\":1 // comment\n}",
	}
	for _, value := range values {
		t.Run(value, func(t *testing.T) {
			if _, err := SetPointer([]byte(`{"a":1}`), "/a", json.RawMessage(value)); err == nil {
				t.Fatal("SetPointer accepted an invalid strict JSON pointer value")
			}
		})
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

func TestSetPointerPreservesBytesOutsideTarget(t *testing.T) {
	doc := []byte("{\n" +
		"  \"zCache\" : [ 1,  2 ],\n" +
		"  \"oauthAccount\": {\"accountUuid\":\"old\"},\n" +
		"  \"projects\":{\"/repo\" : {\"enabled\":true}},\n" +
		"  \"mcpServers\" : { }\n" +
		"}") // Deliberately no trailing newline.
	oldValue := []byte(`{"accountUuid":"old"}`)
	newValue := []byte(`{"accountUuid":"new"}`)

	out, err := SetPointer(doc, "/oauthAccount", newValue)
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Replace(doc, oldValue, newValue, 1)
	if !bytes.Equal(out, want) {
		t.Fatalf("bytes outside pointer changed:\nwant: %q\n got: %q", want, out)
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

func TestSetPointerCreatesEscapedMissingObjectPath(t *testing.T) {
	out, err := SetPointer([]byte(`{"keep": 1}`), "/a~1b/c~0d", json.RawMessage(`"x"`))
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatal(err)
	}
	if v["a/b"].(map[string]any)["c~d"] != "x" || v["keep"] == nil {
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
	// Deleting a missing key is an exact no-op, including formatting and the
	// absence of a trailing newline.
	unchanged, err := DeletePointer([]byte(mixedDoc), "/nope")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(unchanged, []byte(mixedDoc)) {
		t.Fatalf("missing delete rewrote document:\n%s", unchanged)
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

func TestPointerEscapes(t *testing.T) {
	doc := []byte(`{"~":1,"/":2,"~1":3}`)
	for pointer, want := range map[string]string{
		"/~0":  `1`,
		"/~1":  `2`,
		"/~01": `3`,
	} {
		raw, found, err := GetPointer(doc, pointer)
		if err != nil || !found || string(raw) != want {
			t.Errorf("GetPointer(%q) = %s, %v, %v; want %s, true, nil", pointer, raw, found, err, want)
		}
	}
	for _, pointer := range []string{"/~2", "/a~"} {
		if _, _, err := GetPointer(doc, pointer); err == nil {
			t.Errorf("GetPointer accepted malformed escape in %q", pointer)
		}
		if _, err := SetPointer(doc, pointer, json.RawMessage(`4`)); err == nil {
			t.Errorf("SetPointer accepted malformed escape in %q", pointer)
		}
		if _, err := DeletePointer(doc, pointer); err == nil {
			t.Errorf("DeletePointer accepted malformed escape in %q", pointer)
		}
	}
}

func TestWriteFileAtomicEnforcesMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cred.json")
	// an existing world-readable credential file must be tightened on write
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode not enforced: %v", info.Mode())
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
