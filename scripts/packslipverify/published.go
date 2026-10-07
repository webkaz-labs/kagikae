package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func releaseIdentity(tag string) string {
	return "https://github.com/webkaz-labs/kagikae/.github/workflows/release.yml@refs/tags/" + tag
}

func (s *scenario) published(tag string) (any, error) {
	fresh := os.Getenv("KAE_RELEASE_VERIFY_FRESH") == "1"
	settings := ""
	age := "default release-age policy"
	if fresh {
		settings = "[settings]\nminimum_release_age = \"0\"\n"
		age = "isolated zero-age release policy"
	}
	request := func(identity string) string {
		return settings + "[tools]\n" + quote(tool) + " = { version = " + quote(tag[1:]) + ", identity = " + quote(identity) + ", issuer = \"https://token.actions.githubusercontent.com\" }\n"
	}

	// The identity controls run first, each in its own HOME/XDG roots, so neither
	// the accepted install nor recorded pins can satisfy or mask them. One foreign
	// identity is a strict prefix of the release's and the other extends it, so
	// prefix matching in either direction is refused. Requiring mise's anchored
	// mismatch text keeps a refusal for another reason (release age, transport)
	// from passing as an identity check.
	release := releaseIdentity(tag)
	for _, c := range []struct{ name, foreign string }{{"prefix", release[:len(release)-1]}, {"extended", release + "-other"}} {
		name, foreign := c.name, c.foreign
		control := filepath.Join(s.home, "foreign-identity-"+name)
		controlEnv := map[string]string{
			"HOME": control, "XDG_CONFIG_HOME": filepath.Join(control, ".config"), "XDG_DATA_HOME": filepath.Join(control, ".local/share"),
			"XDG_STATE_HOME": filepath.Join(control, ".local/state"), "XDG_CACHE_HOME": filepath.Join(control, ".cache"), "XDG_RUNTIME_DIR": filepath.Join(control, ".local/run"),
		}
		s.write(filepath.Join(controlEnv["XDG_CONFIG_HOME"], "mise/config.toml"), request(foreign), 0o600)
		refusal := s.command(control, "", controlEnv, true, s.mise, "install")
		s.require(strings.Contains(refusal, "identity mismatch: expected "+foreign+", got "), "wrong refusal for a "+name+" foreign signer identity: "+refusal)
		s.command(control, "", controlEnv, true, s.mise, "where", tool)
	}

	s.write(filepath.Join(s.env["XDG_CONFIG_HOME"], "mise/config.toml"), request(release), 0o600)
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
	return map[string]any{"status": "success", "tag": tag, "mise": s.miseVersion, "native_version": version, "trust": "GitHub OIDC exact workflow/tag", "refused": "prefix and extended foreign signer identities", "release_age": age}, s.err
}
