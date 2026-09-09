package main

import (
	"strings"

	"github.com/webkaz-labs/kagikae/tools/devtools/distribution"
)

// Historical archive policy stays in the product adapter.
func releaseSpec(tag, commit string) distribution.Spec {
	s := distribution.Spec{SchemaVersion: 1, Repository: repository, Project: "github.com/" + repository, Tag: tag, Version: strings.TrimPrefix(tag, "v"), Commit: commit, Workflow: ".github/workflows/release.yml", Identity: packslipIdentity(tag), Issuer: "https://token.actions.githubusercontent.com"}
	names, _ := archivesFor(tag)
	for _, name := range names {
		p := strings.Split(name, "_")
		arch := "x86_64"
		if strings.HasPrefix(p[3], "arm64") {
			arch = "aarch64"
		}
		a := distribution.Artifact{Name: name, OS: p[2], Arch: arch, Binary: "kae", Members: []string{"kae", "LICENSE", "README.md"}}
		if p[2] == "linux" {
			a.Libc = "gnu"
		}
		for _, shell := range []string{"bash", "zsh", "fish"} {
			a.Members = append(a.Members, "completions/kae."+shell)
		}
		s.Artifacts = append(s.Artifacts, a)
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		s.Resources = append(s.Resources, map[string]string{"kind": "completion", "shell": shell, "archive": "completions/kae." + shell})
	}
	return s
}
