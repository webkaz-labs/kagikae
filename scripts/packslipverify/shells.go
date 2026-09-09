package main

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

const shellSetup = `
set -e
kae() { command kae version >> "$CALL_LOG"; command kae "$@"; }
cd "$PROJECT_DIR"
`

const bashExercise = `
complete -W custom kae
exercise() {
  COMP_WORDS=(kae ''); COMP_CWORD=1; COMP_LINE='kae '; COMP_POINT=4
  _kae
  printf '%s\n' "${COMPREPLY[@]}"
  COMP_WORDS=(kae use si); COMP_CWORD=2; COMP_LINE='kae use si'; COMP_POINT=10
  _kae
  printf '%s\n' "${COMPREPLY[@]}"
}
`

// As in mise's own fixture, compadd captures candidates without driving a TTY.
const zshExercise = `
autoload -Uz compinit
compinit -i
_custom_kae() { :; }
compdef _custom_kae kae
typeset -A compstate
compstate[nmatches]=0
compadd() { print -rl -- "$@"; }
exercise() {
  words=(kae ''); CURRENT=2
  _kae
  words=(kae use si); CURRENT=3
  _kae
}
`

func (s *scenario) section(text, start, end string) string {
	_, part, ok := strings.Cut(text, start)
	s.require(ok, "missing shell marker: "+start)
	if end != "" {
		part, _, ok = strings.Cut(part, end)
		s.require(ok, "missing shell marker: "+end)
	}
	return part
}

func (s *scenario) verifyShells(root, projectDir string) {
	for _, shell := range []string{"bash", "zsh"} {
		if s.err != nil {
			return
		}
		executable, err := exec.LookPath(shell)
		if err != nil {
			s.err = err
			return
		}
		for _, route := range []string{"auto", "manual"} {
			log := filepath.Join(root, shell+"-"+route+".calls")
			setup := shellSetup
			var loader, restored, expected string
			args := []string{executable}
			if shell == "bash" {
				setup += bashExercise
				loader = "__mise_complete_kae || test \"$?\" = 124\n"
				restored = "complete -p kae\n"
				expected = "-W 'custom' kae"
				args = append(args, "--noprofile", "--norc")
			} else {
				setup += zshExercise
				restored = "print -r -- \"${_comps[kae]}\"\n"
				expected = "_custom_kae"
				args = append(args, "-f")
			}
			var selectScript string
			if route == "auto" {
				setup += "eval \"$(\"$MISE_BIN\" activate " + shell + ")\"\n"
				selectScript = "eval \"$(\"$MISE_BIN\" hook-env -s " + shell + " --force)\"\n" + loader
			} else {
				selectScript = "eval \"$(\"$MISE_BIN\" env -s " + shell + ")\"\neval \"$(\"$MISE_BIN\" completion " + shell + " --tool kae)\"\n"
			}
			script := setup + selectScript + "printf 'FIRST\\n'\nexercise\nprintf 'SWITCH\\n' >> \"$CALL_LOG\"\ncd \"$GLOBAL_DIR\"\n"
			firstEnd := "SECOND\n"
			if route == "manual" {
				script += "eval \"$(\"$MISE_BIN\" env -s " + shell + ")\"\nprintf 'STALE\\n'\nexercise\n"
				firstEnd = "STALE\n"
			}
			script += selectScript + "printf 'SECOND\\n'\nexercise\n"
			secondEnd := ""
			if route == "auto" {
				script += "eval \"$(\"$MISE_BIN\" deactivate)\"\nprintf 'RESTORED\\n'\n" + restored
				secondEnd = "RESTORED\n"
			}
			output := s.command(s.home, script, map[string]string{"CALL_LOG": log, "PROJECT_DIR": projectDir, "GLOBAL_DIR": s.home, "MISE_BIN": s.mise}, false, args...)
			first := s.section(output, "FIRST\n", firstEnd)
			second := s.section(output, "SECOND\n", secondEnd)
			for _, pair := range []struct{ text, version, absent string }{{first, "0.21.0", "0.21.1"}, {second, "0.21.1", "0.21.0"}} {
				s.require(strings.Contains(pair.text, "fixture-static-"+pair.version) && !strings.Contains(pair.text, "fixture-static-"+pair.absent), "static version mismatch: "+shell+" "+route+"\n"+output)
				s.require(slices.Contains(strings.Split(pair.text, "\n"), "side"), "profile completion missing: "+shell+" "+route)
			}
			detail := "custom registration restored"
			if route == "manual" {
				stale := s.section(output, "STALE\n", "SECOND\n")
				s.require(strings.Contains(stale, "fixture-static-0.21.0") && !strings.Contains(stale, "fixture-static-0.21.1"), "raw loading did not retain its snapshot")
				detail = "raw-source reload boundary"
			} else {
				s.require(strings.Contains(s.section(output, "RESTORED\n", ""), expected), "custom registration not restored")
			}
			before, after, ok := strings.Cut(s.read(log), "SWITCH\n")
			s.require(ok, "dynamic call log switch missing")
			for _, pair := range []struct{ text, version string }{{before, "0.21.0"}, {after, "0.21.1"}} {
				lines := strings.Split(strings.TrimSuffix(pair.text, "\n"), "\n")
				s.require(pair.text != "", "dynamic call log empty")
				for _, line := range lines {
					s.require(line == "kae v"+pair.version, "dynamic completion used wrong binary: "+line)
				}
			}
			s.checks = append(s.checks, shell+" same-shell "+route+": static version, dynamic binary/profile and "+detail)
		}
	}
	s.checks = append(s.checks, "fish runtime unverified: resource retrieval only; fish is not required by this fixture")
}
