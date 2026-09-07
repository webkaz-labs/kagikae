package preservation

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

// InspectionIssue names a failed metadata entry. Entry is private filesystem
// data; callers must anonymize it before reporting it.
type InspectionIssue struct{ Entry, Code string }

// Inspect reads metadata without a backend. Pending and deleting records are
// valid metadata; this report makes no claim about their payloads. Mutations
// retain the strict inventory validation in Store.
func Inspect(dir string) ([]Record, []InspectionIssue) {
	records := []Record{}
	issues := []InspectionIssue{}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return records, issues
	}
	if err != nil {
		return records, append(issues, InspectionIssue{Code: constants.ListIssueEnumeration})
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		code := ""
		var r Record
		if !entry.Type().IsRegular() {
			code = constants.ListIssueEntry
		} else {
			data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				code = constants.ListIssueRead
			} else {
				dec := json.NewDecoder(bytes.NewReader(data))
				dec.DisallowUnknownFields()
				if dec.Decode(&r) != nil || dec.Decode(new(any)) != io.EOF || !validRecord(r) || entry.Name() != r.ID+".json" {
					code = constants.ListIssueInvalid
				}
			}
		}
		if code != "" {
			issues = append(issues, InspectionIssue{Entry: entry.Name(), Code: code})
		} else {
			records = append(records, r)
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].ID > records[j].ID
		}
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})
	return records, issues
}
