package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDiagnosticReportPredicates(t *testing.T) {
	cases := []struct {
		kind, good string
		fields     []string
	}{{"auto-report", `{"changed":false,"preserved":[{"tool":"claude","account":"side"}],"results":[]}`, []string{"changed", "preserved", "results"}}, {"backup-report", `{"complete":true,"backups":[{}],"issues":[]}`, []string{"complete", "backups", "issues"}}, {"config-warning-report", `{"complete":true,"warnings":["config_invalid"]}`, []string{"complete", "warnings"}}, {"incomplete-report", `{"complete":false,"backups":[{}],"issues":[{"code":"metadata_invalid"}]}`, []string{"complete", "backups", "issues"}}, {"preservation-report", `{"complete":true,"preservations":[],"warnings":["config_invalid"]}`, []string{"complete", "preservations", "warnings"}}}
	root := t.TempDir()
	path := filepath.Join(root, "report.json")
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(c.good), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := check([]string{c.kind, path}); err != nil {
				t.Fatal(err)
			}
			for _, field := range c.fields {
				for _, value := range []any{nil, "invalid"} {
					var r map[string]any
					if err := json.Unmarshal([]byte(c.good), &r); err != nil {
						t.Fatal(err)
					}
					if value == nil {
						delete(r, field)
					} else {
						r[field] = value
					}
					raw, err := json.Marshal(r)
					if err != nil {
						t.Fatal(err)
					}
					if err := reportCheck(c.kind, raw); err == nil {
						t.Fatalf("missing/invalid %s accepted", field)
					}
				}
			}
			if err := reportCheck(c.kind, []byte(`{broken`)); err == nil {
				t.Fatal("invalid JSON accepted")
			}
		})
	}
	if err := reportCheck("incomplete-report", []byte(`{"complete":false,"backups":[{}],"issues":[{"code":"metadata_invalid"}],"leak":"secret-sentinel"}`)); err == nil {
		t.Fatal("sentinel leak accepted")
	}
	for _, raw := range []string{`{"complete":true,"backups":[],"issues":[]}`, `{"complete":true,"backups":[{}],"issues":[{}]}`} {
		if err := reportCheck("backup-report", []byte(raw)); err == nil {
			t.Fatal("empty backup or issue accepted")
		}
	}
}
