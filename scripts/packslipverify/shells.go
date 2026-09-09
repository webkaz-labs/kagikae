package main

import (
	"github.com/webkaz-labs/kagikae/tools/devtools/completioncheck"
)

func (s *scenario) section(text, start, end string) string {
	result, err := completioncheck.Section(text, start, end)
	if s.err == nil {
		s.err = err
	}
	return result
}

func (s *scenario) verifyShells(root, projectDir string) {
	if s.err != nil {
		return
	}
	checks, err := completioncheck.Lifecycle(completioncheck.Spec{Tool: "kae", Command: "use", Prefix: "si", Candidate: "side", StaticPrefix: "fixture-static-", FirstVersion: "0.21.0", SecondVersion: "0.21.1"}, root, projectDir, s.home, s.mise, func(cwd, input string, extra map[string]string, args ...string) (string, error) {
		out := s.command(cwd, input, extra, false, args...)
		return out, s.err
	})
	if s.err == nil {
		s.err = err
	}
	s.checks = append(s.checks, checks...)
	s.checks = append(s.checks, "fish runtime unverified: resource retrieval only; fish is not required by this fixture")
}
