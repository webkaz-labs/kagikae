package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

func (s *scenario) request(version, target, key string, postinstall bool) {
	fields := "version = " + quote(version) + ", pubkey = " + quote(key) + ", allow_unlogged = true"
	if postinstall {
		fields += `, postinstall = '"$MISE_TOOL_INSTALL_PATH/kae" init'`
	}
	s.write(target, "[tools]\n"+quote(tool)+" = { "+fields+" }\n", 0o600)
}

func (s *scenario) lifecycle(root, global, key string, backend *fixtureServer, defects []string) {
	recipe := filepath.Join(global, "conf.d/kagikae-install.toml")
	s.request("0.21.0", recipe, key, true)
	s.run(s.home, s.mise, "install")
	config := filepath.Join(s.env["XDG_CONFIG_HOME"], "kagikae/config.toml")
	s.require(s.exists(config), "postinstall did not initialize selected config root")
	active := strings.TrimSpace(s.run(s.home, s.mise, "where", tool+"@0.21.0"))
	binary := filepath.Join(active, "kae")
	s.require(strings.TrimSpace(s.run(s.home, binary, "version")) == "kae v0.21.0", "initial version mismatch")
	s.require(!s.exists(filepath.Join(s.env["XDG_STATE_HOME"], "kagikae/installations")), "managed install acquired a direct receipt")
	saved := s.read(config)
	s.remove(config)
	s.run(s.home, s.mise, "install")
	s.require(!s.exists(config), "postinstall ran on a no-op install")
	s.run(s.home, binary, "init")
	saved += "\n[profiles.main.accounts]\nclaude = \"main\"\n[profiles.side.accounts]\nclaude = \"side\"\n"
	s.write(config, saved, 0o600)
	credential := filepath.Join(s.env["XDG_DATA_HOME"], "kagikae/stores/claude/side/fixture-credential")
	const credentialBytes = "fixture credential bytes"
	s.write(credential, credentialBytes, 0o600)
	s.command(s.home, "2\n", nil, false, binary, "completion", "bash", "--install")
	fragment := filepath.Join(global, "conf.d/kagikae.toml")
	fragmentBytes := s.read(fragment)
	custom := filepath.Join(s.env["XDG_DATA_HOME"], "bash-completion/completions/kae")
	s.write(custom, "# fixture custom completion\ncomplete -W custom kae\n", 0o600)
	s.checks = append(s.checks, "initial install, init recipe, no-op and direct-receipt exclusion")

	projectDir := filepath.Join(root, "side-project")
	projectConfig := filepath.Join(projectDir, "mise.toml")
	s.request("0.21.0", projectConfig, key, false)
	s.run(s.home, s.mise, "trust", projectConfig)
	shared := filepath.Join(root, "main-app")
	s.request("0.21.0", filepath.Join(shared, "mise.toml"), key, false)
	s.run(s.home, s.mise, "trust", filepath.Join(shared, "mise.toml"))
	before := s.read(recipe)
	s.run(s.home, s.mise, "use", "--dry-run", "--path", recipe, tool+"@0.21.1")
	s.require(s.read(recipe) == before, "update preview changed configuration")
	s.run(s.home, s.mise, "use", "--path", recipe, tool+"@0.21.1")
	s.require(strings.Contains(s.read(recipe), "postinstall"), "update discarded setup recipe")
	s.require(strings.TrimSpace(s.run(s.home, s.mise, "exec", "--", "kae", "version")) == "kae v0.21.1", "global upgrade mismatch")
	s.require(strings.TrimSpace(s.run(projectDir, s.mise, "exec", "--", "kae", "version")) == "kae v0.21.0", "project selection mismatch")
	s.require(s.read(config) == saved, "upgrade init changed configuration")
	s.require(s.read(fragment) == fragmentBytes, "upgrade changed generated global completion")
	s.require(s.read(credential) == credentialBytes, "upgrade changed credential")
	for _, cwd := range []string{s.home, projectDir} {
		installed := strings.TrimSpace(s.run(cwd, s.mise, "where", tool))
		for _, shell := range []string{"bash", "zsh", "fish"} {
			got := s.run(cwd, s.mise, "completion", shell, "--tool", "kae")
			s.require(got == s.read(filepath.Join(installed, "completions/kae."+shell)), "static completion mismatch: "+shell)
		}
		commands := strings.Split(s.run(cwd, s.mise, "exec", "--", "kae", "__complete", "commands"), "\n")
		s.require(slices.Contains(commands, "uninstall"), "dynamic command completion missing uninstall")
		flags := s.run(cwd, s.mise, "exec", "--", "kae", "__complete", "flags", "uninstall")
		s.require(strings.Contains(flags, "--dir") && strings.Contains(flags, "--dry-run"), "dynamic uninstall flags missing")
		profiles := strings.Split(s.run(cwd, s.mise, "exec", "--", "kae", "__complete", "profiles"), "\n")
		s.require(slices.Contains(profiles, "main") && slices.Contains(profiles, "side"), "dynamic profiles missing")
	}
	beforeOffline := backend.requests.Load()
	got := s.command(shared, "", map[string]string{"MISE_OFFLINE": "1"}, false, s.mise, "exec", "--", "kae", "version")
	s.require(strings.TrimSpace(got) == "kae v0.21.0", "offline version mismatch")
	s.require(backend.requests.Load() == beforeOffline, "offline reuse made an HTTP request")
	s.checks = append(s.checks, "upgrade, project selection and static/dynamic completion")

	report := s.report(s.run(s.home, s.mise, "exec", "--", "kae", "uninstall", "--dry-run", "--json"))
	s.require(report["binary"] == "pending", "managed removal preview must remain pending")
	selected := strings.TrimSpace(s.run(s.home, s.mise, "where", tool))
	partial := s.report(s.command(s.home, "", nil, true, filepath.Join(selected, "kae"), "uninstall", "--yes", "--json"))
	s.require(partial["ok"] == false && partial["integrations"] == "incomplete", "custom integration must report incomplete")
	s.require(!s.exists(fragment), "owned global completion survived teardown")
	s.require(strings.Contains(s.read(custom), "custom"), "custom completion was removed")
	s.require(s.exists(recipe), "user-owned install recipe was removed")
	s.require(s.read(config) == saved && s.read(credential) == credentialBytes, "teardown changed retained data")
	s.verifyShells(root, projectDir)
	s.run(s.home, s.mise, "unuse", "--path", recipe, "--no-prune", tool)
	s.run(s.home, s.mise, "uninstall", tool+"@0.21.1")
	s.require(strings.TrimSpace(s.run(projectDir, s.mise, "exec", "--", "kae", "version")) == "kae v0.21.0", "manager removed a retained project version")
	s.request("0.21.1", recipe, key, true)
	s.run(s.home, s.mise, "install")
	s.require(s.read(config) == saved && s.read(credential) == credentialBytes && !s.exists(fragment), "reinstall changed retained data or recreated hook")
	s.checks = append(s.checks, "managed removal, retained project version and reinstall")

	reasons := map[string]string{"key": "signature does not verify with the pinned key", "project": "the packslip is for", "digest": "document says", "platform": "no artifact for", "missing": "404 Not Found"}
	for index, defect := range defects {
		s.request(fmt.Sprintf("0.21.%d", index+2), recipe, key, true)
		failure := s.command(s.home, "", nil, true, s.mise, "install")
		s.require(strings.Contains(failure, reasons[defect]), "wrong rejection for "+defect+": "+failure)
		s.checks = append(s.checks, "refused "+defect)
	}
	s.require(backend.requests.Load() > 0, "backend did not contact fixture service")
}
