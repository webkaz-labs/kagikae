package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func (s *scenario) published(tag string) (any, error) {
	identity := "https://github.com/webkaz-labs/kagikae/.github/workflows/release.yml@refs/tags/" + tag
	config := filepath.Join(s.env["XDG_CONFIG_HOME"], "mise/config.toml")
	fresh := os.Getenv("KAE_RELEASE_VERIFY_FRESH") == "1"
	settings := ""
	age := "default policy"
	if fresh {
		settings = "[settings]\nminimum_release_age = \"0\"\n"
		age = "explicit isolated zero-age exception"
	}
	s.write(config, settings+"[tools]\n"+quote(tool)+" = { version = "+quote(tag[1:])+", identity = "+quote(identity)+", issuer = \"https://token.actions.githubusercontent.com\" }\n", 0o600)
	s.run(s.home, s.mise, "install")
	installed := strings.TrimSpace(s.run(s.home, s.mise, "where", tool))
	version := strings.TrimSpace(s.run(s.home, s.mise, "exec", "--", "kae", "version"))
	s.require(version == "kae "+tag, "published consumer selected the wrong version")
	for _, shell := range []string{"bash", "zsh", "fish"} {
		got := s.run(s.home, s.mise, "completion", shell, "--tool", "kae")
		s.require(got == s.read(filepath.Join(installed, "completions/kae."+shell)), "published completion resource mismatch: "+shell)
	}
	commands := s.run(s.home, s.mise, "exec", "--", "kae", "__complete", "commands")
	s.require(slices.Contains(strings.Split(commands, "\n"), "uninstall"), "published dynamic command completion is stale")
	return map[string]any{"status": "success", "tag": tag, "mise": strings.TrimSpace(s.run(s.home, s.mise, "--version")), "native_version": version, "trust": "GitHub OIDC exact workflow/tag", "release_age": age}, s.err
}
