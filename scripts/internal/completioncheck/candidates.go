package completioncheck

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/webkaz-labs/kagikae/scripts/internal/commandrun"
	"github.com/webkaz-labs/kagikae/scripts/internal/distribution"
)

type Case struct {
	Words     []string `json:"words"`
	Required  []string `json:"required"`
	Forbidden []string `json:"forbidden"`
	Empty     bool     `json:"empty"`
}
type CandidateSpec struct {
	SchemaVersion int    `json:"schema_version"`
	Tool          string `json:"tool"`
	Function      string `json:"function"`
	Bash          string `json:"bash"`
	Zsh           string `json:"zsh"`
	BinaryDir     string `json:"binary_dir"`
	Cases         []Case `json:"cases"`
}

func Match(candidates []string, test Case) error {
	if test.Empty && len(candidates) != 0 {
		return errors.New("expected empty candidate set")
	}
	for _, want := range test.Required {
		if !slices.Contains(candidates, want) {
			return fmt.Errorf("missing candidate %q", want)
		}
	}
	for _, forbidden := range test.Forbidden {
		if slices.Contains(candidates, forbidden) {
			return fmt.Errorf("forbidden candidate %q", forbidden)
		}
	}
	return nil
}

// Candidates uses a private HOME and real shells with trusted completion scripts.
// Zsh registration is checked separately from the compadd observation seam.
func Candidates(ctx context.Context, s CandidateSpec) (err error) {
	identifier := regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
	if s.SchemaVersion != 1 || !regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`).MatchString(s.Tool) || !identifier.MatchString(s.Function) || len(s.Cases) == 0 {
		return errors.New("invalid completion specification")
	}
	if !filepath.IsAbs(s.BinaryDir) {
		return errors.New("binary_dir must be absolute")
	}
	for _, c := range s.Cases {
		if len(c.Words) == 0 || c.Words[0] != s.Tool || (!c.Empty && len(c.Required) == 0) || c.Empty && len(c.Required) != 0 {
			return errors.New("case needs words and positive or empty expectation")
		}
		for _, values := range [][]string{c.Words, c.Required, c.Forbidden} {
			for _, v := range values {
				if strings.ContainsRune(v, 0) {
					return errors.New("NUL in completion case")
				}
			}
		}
	}
	root, err := os.MkdirTemp("", "completion-check-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, distribution.RemoveWorkdir(root)) }()
	env, err := distribution.IsolatedEnvironment(root)
	if err != nil {
		return err
	}
	env[0] = "PATH=" + s.BinaryDir + ":/usr/bin:/bin"
	for _, shell := range []struct{ name, path string }{{"bash", s.Bash}, {"zsh", s.Zsh}} {
		if !filepath.IsAbs(shell.path) {
			return errors.New("both bash and zsh scripts require absolute paths")
		}
		data, e := os.ReadFile(shell.path)
		if e != nil {
			return e
		}
		scriptPath := filepath.Join(root, shell.name+"-completion")
		if e = os.WriteFile(scriptPath, data, 0o600); e != nil {
			return e
		}
		for _, c := range s.Cases {
			words := []string{}
			for _, v := range c.Words {
				words = append(words, distribution.ShellQuote(v))
			}
			setup := "set -e\n"
			args := []string{"--noprofile", "--norc"}
			if shell.name == "zsh" {
				args = []string{"-f"}
				setup += "autoload -Uz compinit\ncompinit -i\n"
			}
			setup += "source " + distribution.ShellQuote(scriptPath) + "\n"
			if shell.name == "bash" {
				setup += "test \"$(complete -p " + s.Tool + ")\" = " + distribution.ShellQuote("complete -F "+s.Function+" "+s.Tool) + "\n"
				setup += "COMP_WORDS=(" + strings.Join(words, " ") + "); COMP_CWORD=" + fmt.Sprint(len(words)-1) + "\nCOMP_LINE=" + distribution.ShellQuote(strings.Join(c.Words, " ")) + "; COMP_POINT=${#COMP_LINE}\n" + s.Function + "\nif [ \"${#COMPREPLY[@]}\" -gt 0 ]; then printf '%s\\0' \"${COMPREPLY[@]}\"; fi\n"
			} else {
				setup += "test \"${_comps[" + s.Tool + "]}\" = " + distribution.ShellQuote(s.Function) + "\ntypeset -A compstate\ncompstate[nmatches]=0\ncompadd() { if [[ ${1-} != -- ]]; then : > \"$HOME/compadd-rejected\"; return 90; fi; shift; if (( $# > 0 )); then printf '%s\\0' \"$@\"; fi; }\nwords=(" + strings.Join(words, " ") + "); CURRENT=" + fmt.Sprint(len(words)) + "\n" + s.Function + "\n"
			}
			r, e := (commandrun.Command{Name: shell.name, Args: args, Env: env, Dir: root, Stdin: setup, Timeout: time.Minute}).Run(ctx)
			if e != nil {
				return e
			}
			if r.ExitCode != 0 {
				return fmt.Errorf("%s completion driver failed: exit %d", shell.name, r.ExitCode)
			}
			if _, e := os.Lstat(filepath.Join(root, "compadd-rejected")); e == nil {
				return errors.New("unsupported Zsh compadd options")
			} else if !errors.Is(e, os.ErrNotExist) {
				return e
			}
			candidates := []string{}
			if r.Stdout != "" {
				if !strings.HasSuffix(r.Stdout, "\x00") {
					return errors.New("invalid candidate framing")
				}
				candidates = strings.Split(strings.TrimSuffix(r.Stdout, "\x00"), "\x00")
			}
			if e = Match(candidates, c); e != nil {
				return fmt.Errorf("%s: %w", shell.name, e)
			}
		}
	}
	return nil
}
