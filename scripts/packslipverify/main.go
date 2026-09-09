// Command packslipverify exercises mise only inside the canonical smoke HOME.
// Fixture trust and fresh-release exceptions never change the operator's config.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/webkaz-labs/kagikae/scripts/internal/commandrun"
)

const (
	project = "github.com/webkaz-labs/kagikae"
	tool    = "packslip:" + project
)

// scenario retains the first failure; later operations cannot mutate or execute.
// This keeps the lifecycle's assertions adjacent to the operations they guard.
type scenario struct {
	ctx              context.Context
	home, repo, mise string
	env              map[string]string
	err              error
	checks           []string
}

func smokeEnvironment() map[string]string {
	env := map[string]string{"NO_COLOR": "1", "MISE_YES": "1"}
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "MISE_CEILING_PATHS", "GOCACHE", "GOMODCACHE", "GOPATH", "KAE_CLAUDE_DRIVER"} {
		if value, ok := os.LookupEnv(key); ok {
			env[key] = value
		}
	}
	return env
}

func (s *scenario) require(ok bool, message string) {
	if s.err == nil && !ok {
		s.err = errors.New(message)
	}
}

func (s *scenario) command(cwd, input string, extra map[string]string, wantFailure bool, args ...string) string {
	if s.err != nil {
		return ""
	}
	env := make(map[string]string, len(s.env)+len(extra))
	for k, v := range s.env {
		env[k] = v
	}
	for k, v := range extra {
		env[k] = v
	}
	entries := make([]string, 0, len(env))
	for k, v := range env {
		entries = append(entries, k+"="+v)
	}
	slices.Sort(entries)
	result, err := (commandrun.Command{Name: args[0], Args: args[1:], Env: entries, Dir: cwd, Stdin: input, Timeout: 2 * time.Minute}).Run(s.ctx)
	if err != nil {
		s.err = fmt.Errorf("%s: %w", args[0], err)
		return ""
	}
	if (result.ExitCode != 0) != wantFailure {
		s.err = fmt.Errorf("%s %v: exit %d\n%s\n%s", args[0], args[1:], result.ExitCode, result.Stdout, result.Stderr)
		return ""
	}
	if wantFailure {
		return result.Stdout + result.Stderr
	}
	return result.Stdout
}

func (s *scenario) run(cwd string, args ...string) string {
	return s.command(cwd, "", nil, false, args...)
}

func (s *scenario) read(path string) string {
	if s.err != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		s.err = err
	}
	return string(data)
}

func (s *scenario) write(path, data string, mode os.FileMode) {
	if s.err != nil {
		return
	}
	s.err = os.MkdirAll(filepath.Dir(path), 0o700)
	if s.err == nil {
		s.err = os.WriteFile(path, []byte(data), mode)
	}
}

func (s *scenario) copy(from, to string, mode os.FileMode) {
	data := s.read(from)
	s.write(to, data, mode)
}

func (s *scenario) remove(path string) {
	if s.err == nil {
		s.err = os.Remove(path)
	}
}

func (s *scenario) exists(path string) bool {
	if s.err != nil {
		return false
	}
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		s.err = err
	}
	return err == nil
}

func (s *scenario) report(text string) map[string]any {
	result := map[string]any{}
	if s.err == nil {
		s.err = json.Unmarshal([]byte(text), &result)
	}
	return result
}
func quote(value string) string { data, _ := json.Marshal(value); return string(data) }

func newScenario(ctx context.Context) (*scenario, error) {
	home := os.Getenv("HOME")
	if !filepath.IsAbs(home) || !strings.HasPrefix(filepath.Base(home), "kae-smoke-run.") {
		return nil, errors.New("run via scripts/smoke-run.sh; canonical smoke HOME required")
	}
	repo, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	mise, err := exec.LookPath("mise")
	if err != nil {
		return nil, err
	}
	s := &scenario{ctx: ctx, home: home, repo: repo, mise: mise, env: smokeEnvironment()}
	version := s.run(home, mise, "--version")
	s.require(strings.HasPrefix(version, "2026.9.3 "), "consumer requires tested mise 2026.9.3")
	return s, s.err
}

func mainResult(ctx context.Context, args []string) (any, error) {
	if len(args) != 1 && len(args) != 2 {
		return nil, errors.New("usage: packslipverify fixture | published vX.Y.Z")
	}
	if args[0] != "fixture" && args[0] != "published" {
		return nil, errors.New("unknown consumer mode")
	}
	if args[0] == "fixture" && len(args) != 1 {
		return nil, errors.New("fixture takes no tag")
	}
	if args[0] == "published" && (len(args) != 2 || !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(args[1])) {
		return nil, errors.New("expected explicit vX.Y.Z tag")
	}
	s, err := newScenario(ctx)
	if err != nil {
		return nil, err
	}
	if args[0] == "published" {
		return s.published(args[1])
	}
	return s.fixture()
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	result, err := mainResult(ctx, os.Args[1:])
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err = json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
