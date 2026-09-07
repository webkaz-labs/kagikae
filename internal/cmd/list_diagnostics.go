package cmd

import (
	"crypto/sha256"
	"fmt"
	"os"

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
		fmt.Fprintln(os.Stderr, "kae: warning: config is invalid or unreadable; listing metadata from the resolved state directory")
	}
	if !d.Complete {
		fmt.Fprintln(os.Stderr, "kae: metadata listing is incomplete; readable records are shown")
	}
	for _, issue := range d.Issues {
		fmt.Fprintf(os.Stderr, "kae: %s %s\n", issue.Code, issue.Entry)
	}
}
