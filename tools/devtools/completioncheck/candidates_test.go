package completioncheck

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestForeignShellCandidates(t *testing.T) {
	root := t.TempDir()
	bash := filepath.Join(root, "side.bash")
	zsh := filepath.Join(root, "side.zsh")
	for p, text := range map[string]string{bash: `_side() { COMPREPLY=(); case "${COMP_WORDS[1]-}" in empty) :;; --flag=value) COMPREPLY=('with space');; *) COMPREPLY=(side);; esac; }
complete -F _side side
`, zsh: `_side() { case "${words[2]-}" in empty) :;; --flag=value) compadd -- 'with space';; *) compadd -- side;; esac; }
compdef _side side
`} {
		if e := os.WriteFile(p, []byte(text), 0o600); e != nil {
			t.Fatal(e)
		}
	}
	s := CandidateSpec{SchemaVersion: 1, Tool: "side", Function: "_side", Bash: bash, Zsh: zsh, BinaryDir: root, Cases: []Case{{Words: []string{"side", ""}, Required: []string{"side"}, Forbidden: []string{"secret"}}, {Words: []string{"side", "--flag=value"}, Required: []string{"with space"}}, {Words: []string{"side", "empty"}, Empty: true}}}
	if e := Candidates(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	s.Cases[0].Forbidden = []string{"side"}
	if e := Candidates(context.Background(), s); e == nil {
		t.Fatal("forbidden output accepted")
	}
	s.Cases = []Case{{Words: []string{"side", "empty"}, Required: []string{"side"}}}
	if e := Candidates(context.Background(), s); e == nil {
		t.Fatal("empty output accepted as positive")
	}
}

func TestZshOptionsAreNotCandidates(t *testing.T) {
	root := t.TempDir()
	bash := filepath.Join(root, "bash")
	zsh := filepath.Join(root, "zsh")
	for p, text := range map[string]string{bash: "_side() { COMPREPLY=(descriptions); }\ncomplete -F _side side\n", zsh: "_side() { compadd -d descriptions -- actual || true; }\ncompdef _side side\n"} {
		if e := os.WriteFile(p, []byte(text), 0o600); e != nil {
			t.Fatal(e)
		}
	}
	s := CandidateSpec{SchemaVersion: 1, Tool: "side", Function: "_side", Bash: bash, Zsh: zsh, BinaryDir: root, Cases: []Case{{Words: []string{"side", ""}, Required: []string{"descriptions"}}}}
	if e := Candidates(context.Background(), s); e == nil {
		t.Fatal("option operand accepted as candidate")
	}
}
