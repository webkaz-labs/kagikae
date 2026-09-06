// Command storecheck checks the fixed JSON and fragment assertions in the
// per-account release smoke. It reads synthetic files supplied by that block.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

func check(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("expected check and fixture path")
	}
	kind, path := args[0], args[1]
	if kind == "credential" || kind == "removed" {
		path = filepath.Join(path, ".credentials.json")
	}
	data, err := os.ReadFile(path)
	if kind == "removed" && os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	switch kind {
	case "fragment":
		if len(args) != 3 {
			return fmt.Errorf("expected fragment key")
		}
		prefix := args[2] + " = "
		matches := 0
		var value string
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, prefix) {
				matches++
				value, err = strconv.Unquote(strings.TrimPrefix(line, prefix))
				if err != nil {
					return err
				}
			}
		}
		if matches != 1 || value == "" {
			return fmt.Errorf("expected one nonempty fragment value")
		}
		fmt.Println(value)
	case "credential", "removed":
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(data, &doc); err != nil {
			return err
		}
		if doc == nil {
			return fmt.Errorf("expected credential object")
		}
		payload, present := doc["claudeAiOauth"]
		if kind == "removed" {
			if present {
				return fmt.Errorf("credential member remains")
			}
			return nil
		}
		if len(args) != 3 || !present {
			return fmt.Errorf("expected credential and token")
		}
		var credential struct {
			AccessToken string `json:"accessToken"`
		}
		if err := json.Unmarshal(payload, &credential); err != nil {
			return err
		}
		if credential.AccessToken != args[2] {
			return fmt.Errorf("credential token mismatch")
		}
	case "normal", "unsplit":
		var report struct {
			Schema int                                      `json:"schema_version"`
			Checks []struct{ Code, Status, Message string } `json:"checks"`
		}
		if err := json.Unmarshal(data, &report); err != nil {
			return err
		}
		if report.Schema != constants.SchemaVersion || report.Checks == nil {
			return fmt.Errorf("expected doctor report")
		}
		count := 0
		for _, row := range report.Checks {
			if row.Code != constants.CheckCredentialUnsplit {
				continue
			}
			count++
			if kind == "unsplit" {
				if len(args) != 3 || row.Status != constants.StatusWarn || !strings.Contains(row.Message, "cd "+args[2]+" && kae pin") {
					return fmt.Errorf("unexpected migration diagnostic")
				}
			}
		}
		if (kind == "normal" && count != 0) || (kind == "unsplit" && count != 1) {
			return fmt.Errorf("unexpected migration finding count: %d", count)
		}
	default:
		return fmt.Errorf("unknown smoke check")
	}
	return nil
}

func main() {
	if err := check(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
