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
    "login": "webkaz"
  },
  "loggedInUsers": [
    {
      "host": "https://github.com",
      "login": "webkaz"
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
	if got["host"] != "https://github.com" || got["login"] != "webkaz" {
		t.Fatalf("unexpected value: %s", raw)
	}
}

func TestSetPointerJSONCPreservesCommentsAndSiblings(t *testing.T) {
	out, err := SetPointerJSONC([]byte(copilotConfig), "/lastLoggedInUser",
		json.RawMessage(`{"host":"https://github.com","login":"personal"}`))
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
	if !strings.Contains(string(got), `"personal"`) {
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
