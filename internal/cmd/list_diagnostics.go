package cmd

import (
	"crypto/sha256"
	"fmt"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

type listIssue struct {
	Entry string `json:"entry,omitempty"`
	Code  string `json:"code"`
}

type listDiagnostics struct {
	Complete bool        `json:"complete"`
	Issues   []listIssue `json:"issues"`
	Warnings []string    `json:"warnings"`
}

func newListDiagnostics(app *App) listDiagnostics {
	d := listDiagnostics{Complete: true, Issues: []listIssue{}, Warnings: []string{}}
	if app.ConfigErr != nil {
		d.Warnings = append(d.Warnings, constants.ListWarningConfig)
	}
	return d
}

func (d *listDiagnostics) add(entry, code string) {
	issue := listIssue{Code: code}
	if entry != "" {
		issue.Entry = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(entry)))
	}
	d.Issues = append(d.Issues, issue)
	d.Complete = false
}

func (d listDiagnostics) exitCode() int {
	if !d.Complete {
		return constants.ExitError
	}
	return constants.ExitOK
}

func (d listDiagnostics) print() {
	for range d.Warnings {
		warnf("config is invalid or unreadable; listing metadata from the resolved state directory; check the selected config file and its permissions before recovery; run: kae doctor")
	}
	if !d.Complete {
		infof("metadata listing is incomplete; readable records are shown")
	}
	for _, issue := range d.Issues {
		infof("%s %s; %s", issue.Code, issue.Entry, listIssueGuidance(issue.Code))
	}
}

// listIssueGuidance is the remedy for an issue code, as a message so the line
// renders in the selected language (the code and the entry stay tokens).
func listIssueGuidance(code string) message {
	switch code {
	case constants.ListIssueEnumeration:
		return msgf("check the resolved state directory exists as a directory and is accessible; see docs/CLI.md Recovery guidance")
	case constants.ListIssueRead:
		return msgf("check metadata file and parent-directory permissions; keep the entry while investigating")
	case constants.ListIssueInvalid:
		return msgf("check metadata format against docs/DATA-MODEL.md; do not infer an account or delete the entry to clear this warning")
	case constants.ListIssueEntry:
		return msgf("inspect the entry type without following symlinks; keep unexpected entries until their purpose is verified")
	default:
		return msgf("see docs/CLI.md Recovery guidance before recovery")
	}
}
