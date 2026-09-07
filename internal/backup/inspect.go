package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

// InspectionIssue names a failed metadata entry. Entry is private filesystem
// data; callers must anonymize it before reporting it.
type InspectionIssue struct{ Entry, Code string }

// Inspect lists readable metadata alongside failures. Mutation callers must
// continue using List, which refuses an incomplete inventory.
func Inspect(dir string) ([]Meta, []InspectionIssue) {
	metas := []Meta{}
	issues := []InspectionIssue{}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return metas, issues
	}
	if err != nil {
		return metas, append(issues, InspectionIssue{Code: constants.ListIssueEnumeration})
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		code := ""
		var meta Meta
		if !entry.Type().IsRegular() {
			code = constants.ListIssueEntry
		} else {
			data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				code = constants.ListIssueRead
			} else if json.Unmarshal(data, &meta) != nil || meta.ID != strings.TrimSuffix(entry.Name(), ".json") || meta.CreatedAt.IsZero() || meta.SchemaVersion != constants.SchemaVersion {
				code = constants.ListIssueInvalid
			}
		}
		if code != "" {
			issues = append(issues, InspectionIssue{Entry: entry.Name(), Code: code})
		} else {
			metas = append(metas, meta)
		}
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].ID > metas[j].ID })
	return metas, issues
}
