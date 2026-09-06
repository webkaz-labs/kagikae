package main

import (
	"os"
	"path/filepath"
	"testing"
)

// These controls keep malformed evidence, token mismatches and migration
// diagnostics distinct from the matching positive fixture.
func TestSmokeEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		args       []string
		valid      bool
	}{
		{"malformed", "{", []string{"credential", "", "main"}, false},
		{"wrong-token", `{"claudeAiOauth":{"accessToken":"side"}}`, []string{"credential", "", "main"}, false},
		{"missing-checks", `{"schema_version":1}`, []string{"normal", ""}, false},
		{"retained", `{"claudeAiOauth":{}}`, []string{"removed", ""}, false},
		{"unsplit", `{"schema_version":1,"checks":[{"code":"credential_unsplit","status":"warn","message":"cd /main-app && kae pin"}]}`, []string{"unsplit", "", "/main-app"}, true},
		{"unsplit-status", `{"schema_version":1,"checks":[{"code":"credential_unsplit","status":"ok","message":"cd /main-app && kae pin"}]}`, []string{"unsplit", "", "/main-app"}, false},
		{"unsplit-path", `{"schema_version":1,"checks":[{"code":"credential_unsplit","status":"warn","message":"cd /side-project && kae pin"}]}`, []string{"unsplit", "", "/main-app"}, false},
		{"unsplit-missing", `{"schema_version":1,"checks":[]}`, []string{"unsplit", "", "/main-app"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			file := filepath.Join(root, ".credentials.json")
			if err := os.WriteFile(file, []byte(tc.data), 0o600); err != nil {
				t.Fatal(err)
			}
			tc.args[1] = root
			if tc.args[0] == "normal" || tc.args[0] == "unsplit" {
				tc.args[1] = file
			}
			if err := check(tc.args); (err == nil) != tc.valid {
				t.Fatalf("check error = %v; want valid=%v", err, tc.valid)
			}
		})
	}
}
