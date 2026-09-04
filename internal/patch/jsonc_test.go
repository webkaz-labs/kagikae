package patch

import (
	"encoding/json"
	"strings"
	"testing"
)

const copilotConfig = `// User settings belong in settings.json.
// This file is managed automatically.
{
  "firstLaunchAt": "2026-03-13T11:08:27.774Z",
  "trustedFolders": [
    "/workspaces"
  ],
  "lastLoggedInUser": {
    "host": "https://github.com",
    "login": "main"
  },
  "loggedInUsers": [
    {
      "host": "https://github.com",
      "login": "main"
    }
  ]
}
`

func TestGetPointerJSONCIgnoresComments(t *testing.T) {
	raw, found, err := GetPointerJSONC([]byte(copilotConfig), "/lastLoggedInUser")
	if err != nil || !found {
		t.Fatalf("get: %v found=%v", err, found)
	}
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["host"] != "https://github.com" || got["login"] != "main" {
		t.Fatalf("unexpected value: %s", raw)
	}
}

func TestSetPointerJSONCPreservesCommentsAndSiblings(t *testing.T) {
	out, err := SetPointerJSONC([]byte(copilotConfig), "/lastLoggedInUser",
		json.RawMessage(`{"host":"https://github.com","login":"side"}`))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	// Comments survive.
	if !strings.Contains(s, "// User settings belong in settings.json.") ||
		!strings.Contains(s, "// This file is managed automatically.") {
		t.Fatalf("leading comments lost:\n%s", s)
	}
	// The targeted value changed.
	got, found, err := GetPointerJSONC(out, "/lastLoggedInUser")
	if err != nil || !found {
		t.Fatalf("re-read: %v found=%v", err, found)
	}
	if !strings.Contains(string(got), `"side"`) {
		t.Fatalf("value not switched: %s", got)
	}
	// Siblings survive untouched.
	for _, want := range []string{"/firstLaunchAt", "/trustedFolders", "/loggedInUsers"} {
		if _, found, _ := GetPointerJSONC(out, want); !found {
			t.Fatalf("sibling %s lost:\n%s", want, s)
		}
	}
}

func TestSetPointerJSONCCreatesMissingMember(t *testing.T) {
	out, err := SetPointerJSONC([]byte(copilotConfig), "/newKey", json.RawMessage(`"v"`))
	if err != nil {
		t.Fatal(err)
	}
	if raw, found, _ := GetPointerJSONC(out, "/newKey"); !found || string(raw) != `"v"` {
		t.Fatalf("member not created: %s", raw)
	}
	if !strings.Contains(string(out), "// This file is managed automatically.") {
		t.Fatal("comments lost on create")
	}
}

func TestSetPointerJSONCPreservesTrailingComma(t *testing.T) {
	doc := []byte("// comment\n{\n  \"a\": 1,\n}\n")
	out, err := SetPointerJSONC(doc, "/a", json.RawMessage(`2`))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "// comment\n{\n  \"a\": 2,\n}\n" {
		t.Fatalf("JSONC formatting changed:\n%s", out)
	}
}

func TestDeletePointerJSONC(t *testing.T) {
	out, err := DeletePointerJSONC([]byte(copilotConfig), "/lastLoggedInUser")
	if err != nil {
		t.Fatal(err)
	}
	if _, found, _ := GetPointerJSONC(out, "/lastLoggedInUser"); found {
		t.Fatalf("member not removed:\n%s", out)
	}
	if !strings.Contains(string(out), "// User settings belong in settings.json.") {
		t.Fatal("comments lost on delete")
	}
	// Absent pointer is a no-op, not an error.
	out2, err := DeletePointerJSONC(out, "/lastLoggedInUser")
	if err != nil {
		t.Fatalf("absent delete should be a no-op: %v", err)
	}
	if string(out2) != string(out) {
		t.Fatal("absent delete changed the document")
	}
}

func TestJSONCRejectsBrokenInput(t *testing.T) {
	if _, _, err := GetPointerJSONC([]byte(`{not json`), "/x"); err == nil {
		t.Fatal("expected parse error on read")
	}
	if _, err := SetPointerJSONC([]byte(`{not json`), "/x", json.RawMessage(`1`)); err == nil {
		t.Fatal("expected parse error on write")
	}
}

func TestJSONCPointerValidationIsConsistent(t *testing.T) {
	doc := []byte(`{"a":1}`)
	for _, pointer := range []string{"", "/~2", "/a~"} {
		t.Run(pointer, func(t *testing.T) {
			if _, _, err := GetPointerJSONC(doc, pointer); err == nil {
				t.Fatal("GetPointerJSONC accepted an invalid pointer")
			}
			if _, err := SetPointerJSONC(doc, pointer, json.RawMessage(`2`)); err == nil {
				t.Fatal("SetPointerJSONC accepted an invalid pointer")
			}
			if _, err := DeletePointerJSONC(doc, pointer); err == nil {
				t.Fatal("DeletePointerJSONC accepted an invalid pointer")
			}
		})
	}

	escapedDoc := []byte(`{"~":1,"/":2,"~1":3,}`)
	for pointer, want := range map[string]string{
		"/~0":  `1`,
		"/~1":  `2`,
		"/~01": `3`,
	} {
		raw, found, err := GetPointerJSONC(escapedDoc, pointer)
		if err != nil || !found || string(raw) != want {
			t.Errorf("GetPointerJSONC(%q) = %s, %v, %v; want %s, true, nil", pointer, raw, found, err, want)
		}
	}
}

func TestJSONCRejectsDuplicateMembersAtAnyDepth(t *testing.T) {
	documents := []string{
		"// comment\n{\"a\":1,\"a\":2,}",
		"{\"outer\":{\"a\":1,\"a\":2,},}",
	}
	for _, doc := range documents {
		t.Run(doc, func(t *testing.T) {
			if _, _, err := GetPointerJSONC([]byte(doc), "/a"); err == nil {
				t.Fatal("GetPointerJSONC accepted duplicate object members")
			}
			if _, err := SetPointerJSONC([]byte(doc), "/x", json.RawMessage(`1`)); err == nil {
				t.Fatal("SetPointerJSONC accepted duplicate object members")
			}
			if _, err := DeletePointerJSONC([]byte(doc), "/missing"); err == nil {
				t.Fatal("DeletePointerJSONC accepted duplicate object members")
			}
		})
	}
}

func TestSetPointerJSONCRejectsInvalidStrictJSONValue(t *testing.T) {
	values := []string{
		`1 2`,
		`{"a":1,"a":2}`,
		`{"outer":{"a":1,"a":2}}`,
		"{\"a\":1 // comment\n}",
		`{"a":1,}`,
	}
	for _, value := range values {
		t.Run(value, func(t *testing.T) {
			if _, err := SetPointerJSONC([]byte(copilotConfig), "/lastLoggedInUser", json.RawMessage(value)); err == nil {
				t.Fatal("SetPointerJSONC accepted an invalid strict JSON pointer value")
			}
		})
	}
}
