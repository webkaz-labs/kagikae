package main

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
)

// reportCheck preserves the fixed daily-diagnostics smoke predicates. Missing
// fields are not interchangeable with false or empty arrays.
func reportCheck(kind string, data []byte) error {
	var r map[string]any
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	empty := []any{}
	warnings := []any{"config_invalid"}
	matches := false
	switch kind {
	case "auto-report":
		matches = r["changed"] == false && reflect.DeepEqual(r["preserved"], []any{map[string]any{"tool": "claude", "account": "side"}}) && reflect.DeepEqual(r["results"], empty)
	case "backup-report":
		backups, ok := r["backups"].([]any)
		matches = r["complete"] == true && ok && len(backups) > 0 && reflect.DeepEqual(r["issues"], empty)
	case "config-warning-report":
		matches = r["complete"] == true && reflect.DeepEqual(r["warnings"], warnings)
	case "incomplete-report":
		backups, ok := r["backups"].([]any)
		issues, okIssues := r["issues"].([]any)
		var code any
		if okIssues && len(issues) > 0 {
			if first, ok := issues[0].(map[string]any); ok {
				code = first["code"]
			}
		}
		matches = r["complete"] == false && ok && len(backups) > 0 && code == "metadata_invalid" && !strings.Contains(string(data), "secret-sentinel")
	case "preservation-report":
		matches = r["complete"] == true && reflect.DeepEqual(r["preservations"], empty) && reflect.DeepEqual(r["warnings"], warnings)
	}
	if !matches {
		return errors.New("diagnostic smoke report mismatch")
	}
	return nil
}
