// Package distribution verifies trusted release expectations before execution.
// Product policy is supplied by adapters, never inferred from downloaded metadata.
package distribution

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
)

type Artifact struct {
	Name    string   `json:"name"`
	OS      string   `json:"os"`
	Arch    string   `json:"arch"`
	Libc    string   `json:"libc,omitempty"`
	Binary  string   `json:"binary"`
	Members []string `json:"members"`
}

func (a Artifact) MemberSet() map[string]bool {
	m := map[string]bool{}
	for _, v := range a.Members {
		m[v] = true
	}
	return m
}

type Spec struct {
	SchemaVersion int                 `json:"schema_version"`
	Repository    string              `json:"repository"`
	Project       string              `json:"project"`
	Tag           string              `json:"tag"`
	Version       string              `json:"version"`
	Commit        string              `json:"commit"`
	Workflow      string              `json:"workflow"`
	Identity      string              `json:"identity"`
	Issuer        string              `json:"issuer"`
	Artifacts     []Artifact          `json:"artifacts"`
	Resources     []map[string]string `json:"resources"`
}

var (
	identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
	commit     = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

func safePath(v string) bool {
	if v == "" || path.IsAbs(v) || path.Clean(v) != v {
		return false
	}
	for _, p := range strings.Split(v, "/") {
		if !regexp.MustCompile(`^[a-zA-Z0-9._-]+$`).MatchString(p) || p == ".." || p == "." {
			return false
		}
	}
	return true
}

func (s Spec) Validate() error {
	if s.SchemaVersion != 1 || !safePath(s.Repository) || strings.Count(s.Repository, "/") != 1 || !identifier.MatchString(s.Tag) || !identifier.MatchString(s.Version) || !commit.MatchString(s.Commit) || !safePath(s.Workflow) || s.Project == "" || s.Identity == "" || s.Issuer == "" {
		return errors.New("invalid release specification identity")
	}
	if s.Identity != "https://github.com/"+s.Repository+"/"+s.Workflow+"@refs/tags/"+s.Tag || s.Issuer != "https://token.actions.githubusercontent.com" {
		return errors.New("expected exact GitHub workflow identity and issuer")
	}
	if len(s.Artifacts) == 0 {
		return errors.New("empty artifact set")
	}
	seen := map[string]bool{}
	for _, a := range s.Artifacts {
		if !identifier.MatchString(a.Name) || !strings.HasSuffix(a.Name, ".tar.gz") || seen[a.Name] || !safePath(a.Binary) || a.OS == "" || a.Arch == "" {
			return errors.New("invalid or duplicate artifact")
		}
		seen[a.Name] = true
		members := a.MemberSet()
		if len(members) != len(a.Members) || !members[a.Binary] {
			return errors.New("duplicate members or missing binary")
		}
		for name := range members {
			if !safePath(name) {
				return errors.New("unsafe archive member")
			}
		}
	}
	for i, r := range s.Resources {
		if r["kind"] != "completion" || !safePath(r["archive"]) || (r["shell"] != "bash" && r["shell"] != "zsh" && r["shell"] != "fish") || len(r) != 3 {
			return errors.New("invalid completion resource")
		}
		for _, a := range s.Artifacts {
			if !a.MemberSet()[r["archive"]] {
				return errors.New("resource missing from archive members")
			}
		}
		for _, prev := range s.Resources[:i] {
			if prev["shell"] == r["shell"] {
				return errors.New("duplicate completion resource")
			}
		}
	}
	return nil
}

func ParseSpec(raw []byte) (Spec, error) {
	var s Spec
	if len(raw) > 1024*1024 {
		return s, errors.New("spec too large")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&s); err != nil {
		return s, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return s, fmt.Errorf("trailing specification data")
	}
	return s, s.Validate()
}
