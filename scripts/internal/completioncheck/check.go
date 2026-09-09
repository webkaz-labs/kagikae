// Package completioncheck observes actual Bash/Zsh completion output. Zsh uses
// compadd capture, not a terminal editor or a claim about interactive Tab behavior.
package completioncheck

import (
	"errors"
	"os"
	"regexp"
	"strings"
)

type (
	Spec    struct{ Tool, Command, Prefix, Candidate, StaticPrefix, FirstVersion, SecondVersion string }
	Execute func(cwd, input string, env map[string]string, args ...string) (string, error)
	harness struct {
		spec       Spec
		home, mise string
		err        error
		checks     []string
		execute    Execute
	}
)

func (s *harness) require(ok bool, message string) {
	if s.err == nil && !ok {
		s.err = errors.New(message)
	}
}

func (s *harness) command(cwd, input string, extra map[string]string, _ bool, args ...string) string {
	if s.err != nil {
		return ""
	}
	out, err := s.execute(cwd, input, extra, args...)
	s.err = err
	return out
}

func (s *harness) read(path string) string {
	if s.err != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	s.err = err
	return string(data)
}

func Section(text, start, end string) (string, error) {
	_, part, ok := strings.Cut(text, start)
	if !ok {
		return "", errors.New("missing shell marker: " + start)
	}
	if end != "" {
		part, _, ok = strings.Cut(part, end)
		if !ok {
			return "", errors.New("missing shell marker: " + end)
		}
	}
	return part, nil
}

func Lifecycle(spec Spec, root, project, home, mise string, run Execute) ([]string, error) {
	for _, v := range []string{spec.Tool, spec.Command, spec.Prefix, spec.StaticPrefix, spec.FirstVersion, spec.SecondVersion} {
		if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`).MatchString(v) {
			return nil, errors.New("unsafe shell fixture identifier")
		}
	}
	if spec.Candidate == "" || strings.ContainsAny(spec.Candidate, "\r\n") {
		return nil, errors.New("expected a nonempty candidate")
	}
	s := &harness{spec: spec, home: home, mise: mise, execute: run}
	s.verifyShells(root, project)
	return s.checks, s.err
}
