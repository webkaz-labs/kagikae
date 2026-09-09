package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/scripts/internal/commandrun"
	"github.com/webkaz-labs/kagikae/scripts/internal/completioncheck"
	"github.com/webkaz-labs/kagikae/scripts/internal/distribution"
)

func TestBuiltCLIFromForeignDirectory(t *testing.T) {
	root := t.TempDir()
	goBinary, e := exec.LookPath("go")
	if e != nil {
		t.Fatal(e)
	}
	cwd, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	binary := filepath.Join(root, "completionverify")
	r, e := (commandrun.Command{Name: goBinary, Args: []string{"build", "-o", binary, "."}, Env: os.Environ(), Dir: cwd, Timeout: time.Minute}).Run(context.Background())
	if e != nil || r.ExitCode != 0 {
		t.Fatal(e, r.Stderr)
	}
	bash := filepath.Join(root, "side.bash")
	zsh := filepath.Join(root, "side.zsh")
	for p, v := range map[string]string{bash: "_side() { COMPREPLY=('with space'); }\ncomplete -F _side side\n", zsh: "_side() { compadd -- 'with space'; }\ncompdef _side side\n"} {
		if e = os.WriteFile(p, []byte(v), 0o600); e != nil {
			t.Fatal(e)
		}
	}
	s := completioncheck.CandidateSpec{SchemaVersion: 1, Tool: "side", Function: "_side", Bash: bash, Zsh: zsh, BinaryDir: root, Cases: []completioncheck.Case{{Words: []string{"side", "--flag=value"}, Required: []string{"with space"}}}}
	raw, e := json.Marshal(s)
	if e != nil {
		t.Fatal(e)
	}
	spec := filepath.Join(root, "spec.json")
	if e = os.WriteFile(spec, raw, 0o600); e != nil {
		t.Fatal(e)
	}
	env, e := distribution.IsolatedEnvironment(root)
	if e != nil {
		t.Fatal(e)
	}
	r, e = (commandrun.Command{Name: binary, Args: []string{spec}, Env: env, Dir: root, Timeout: time.Minute}).Run(context.Background())
	if e != nil || r.ExitCode != 0 {
		t.Fatal(e, r.Stderr, r.Stdout)
	}
	var report struct{ Status, Fish string }
	if e = json.Unmarshal([]byte(r.Stdout), &report); e != nil || report.Status != "success" || report.Fish != "unverified" {
		t.Fatal(e, r.Stdout)
	}
}
