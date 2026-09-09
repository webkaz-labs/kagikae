// Package glossary reads terms under explicitly selected Markdown headings.
package glossary

import (
	"os"
	"regexp"
	"strings"
)

var (
	sectionLine   = regexp.MustCompile(`^##\s+(.*?)\s*$`)
	parenthetical = regexp.MustCompile(`\([^)]*\)`)
	termShape     = regexp.MustCompile("^[A-Za-z][A-Za-z ._-]*$")
)

func Read(path string, sections map[string]bool) ([]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	section := ""
	for _, line := range strings.Split(string(body), "\n") {
		if m := sectionLine.FindStringSubmatch(line); m != nil {
			section = m[1]
			continue
		}
		if !sections[section] || !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		cells := strings.Split(strings.TrimSpace(line), "|")
		if len(cells) < 2 {
			continue
		}
		cell := parenthetical.ReplaceAllString(cells[1], " ")
		for _, piece := range strings.Split(cell, ",") {
			term := strings.TrimSpace(strings.Trim(strings.TrimSpace(piece), "*`"))
			if len(term) >= 4 && term != "term" && termShape.MatchString(term) {
				out = append(out, term)
			}
		}
	}
	return out, nil
}
