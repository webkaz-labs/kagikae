package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/runner"
	"github.com/webkaz-labs/kagikae/internal/testutil/runnertest"
)

const (
	testTMPDIRChildEnv = "KAE_TEST_TMPDIR_CHILD"
	testTMPDIRChildRan = "kae-test-tmpdir-child-ran"
	testGitOverrideEnv = "KAE_TEST_GIT_OVERRIDE"
)

// TestMain closes two process-wide holes that a per-test fixture cannot close:
// t.TempDir must never land inside a repository whose info/exclude a pin can
// append to, and every subprocess seam starts fail-loud until a test opts in.
func TestMain(m *testing.M) {
	// These variables select a repository independently of the process cwd. Clear
	// them for the process, not only for the probe below: ensureGitExcluded uses
	// real git during tests and would otherwise mutate the caller-selected repo.
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE"} {
		if err := os.Unsetenv(key); err != nil {
			fmt.Fprintf(os.Stderr, "kae tests: cannot clear %s: %v\n", key, err)
			os.Exit(1)
		}
	}
	if root, inside, err := tempDirGitWorktree(os.TempDir()); err != nil {
		fmt.Fprintf(os.Stderr, "kae tests: cannot verify TMPDIR isolation: %v\n", err)
		os.Exit(1)
	} else if inside {
		fmt.Fprintf(os.Stderr,
			"kae tests: refusing TMPDIR %q inside git worktree %q; tests can append to that repository's .git/info/exclude\n",
			os.TempDir(), root)
		os.Exit(1)
	}

	savedDefault := runner.Default
	savedInteractive := runner.RunInteractive
	savedWithEnv := runner.RunWithEnv
	runner.Default = cmdTestRunnerGuard{next: savedDefault}
	runner.RunInteractive = func(_ context.Context, _ []string, name string, args ...string) (int, error) {
		panicUnstubbedRunner("runner.RunInteractive", name, args)
		return 1, nil
	}
	runner.RunWithEnv = func(_ context.Context, _ []string, name string, args ...string) (string, string, int) {
		panicUnstubbedRunner("runner.RunWithEnv", name, args)
		return "", "", 1
	}

	code := m.Run()
	runner.Default = savedDefault
	runner.RunInteractive = savedInteractive
	runner.RunWithEnv = savedWithEnv
	os.Exit(code)
}

// tempDirGitWorktree asks the same git binary ensureGitExcluded will ask, from
// the directory t.TempDir uses. GIT_DIR and GIT_WORK_TREE are deliberately
// removed: they describe a caller-selected repository, not whether temp itself
// is nested in one. A non-zero git answer is the safe answer for this boundary;
// ensureGitExcluded gets the same non-zero answer and writes nowhere.
func tempDirGitWorktree(dir string) (root string, inside bool, err error) {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	cmd.Env = envWithoutGitRepositoryOverrides(os.Environ())
	out, runErr := cmd.Output()
	if runErr == nil {
		root = strings.TrimSpace(string(out))
		if root == "" {
			return "", false, errors.New("git returned an empty worktree root")
		}
		return root, true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) || errors.Is(runErr, exec.ErrNotFound) {
		return "", false, nil
	}
	return "", false, runErr
}

func envWithoutGitRepositoryOverrides(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		if strings.HasPrefix(entry, "GIT_DIR=") || strings.HasPrefix(entry, "GIT_WORK_TREE=") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

// cmdTestRunnerGuard is the package baseline, not a fixture reply. Credential
// programs panic until a test explicitly supplies a runner; other programs
// delegate because ensureGitExcluded deliberately measures a real repository.
type cmdTestRunnerGuard struct {
	next runner.Runner
}

func (g cmdTestRunnerGuard) Run(ctx context.Context, name string, args ...string) (string, string, int) {
	if isCredentialProgram(name) {
		panicUnstubbedRunner("runner.Default.Run", name, args)
	}
	return g.next.Run(ctx, name, args...)
}

func (g cmdTestRunnerGuard) RunInput(ctx context.Context, stdin, name string, args ...string) (string, string, int) {
	if isCredentialProgram(name) {
		panicUnstubbedRunner("runner.Default.RunInput", name, args)
	}
	return g.next.RunInput(ctx, stdin, name, args...)
}

func isCredentialProgram(name string) bool {
	switch filepath.Base(name) {
	case "security", "secret-tool":
		return true
	default:
		return false
	}
}

func panicUnstubbedRunner(seam, name string, args []string) {
	panic(fmt.Sprintf("kae test used %s without a fixture: argv=%s", seam, safeTestArgv(name, args)))
}

// safeTestArgv preserves argument boundaries but never repeats security's -w
// value. That CLI is the one unavoidable production path that receives a
// credential in argv. stdin and extraEnv never enter this function.
func safeTestArgv(name string, args []string) string {
	safe := append([]string(nil), args...)
	if filepath.Base(name) == "security" {
		for i := 0; i+1 < len(safe); i++ {
			if safe[i] == "-w" {
				safe[i+1] = "[redacted]"
				i++
			}
		}
	}
	return fmt.Sprintf("%q", append([]string{name}, safe...))
}

func TestTMPDIRGuardRejectsAWorktreeBeforeTestsRun(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	gitInit(t, repo)

	out, err := runTMPDIRChild(t, repo, nil)
	if err == nil {
		t.Fatalf("child tests ran with TMPDIR inside a worktree:\n%s", out)
	}
	if !strings.Contains(out, "refusing TMPDIR") || !strings.Contains(out, repo) {
		t.Fatalf("child did not fail at the TMPDIR boundary: %v\n%s", err, out)
	}
	if strings.Contains(out, testTMPDIRChildRan) {
		t.Fatalf("child test body ran after the TMPDIR refusal:\n%s", out)
	}
}

func TestTMPDIRGuardIgnoresInheritedGitRepositoryOverrides(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	gitInit(t, repo)
	outside := t.TempDir()
	exclude := filepath.Join(repo, ".git", "info", "exclude")
	before, err := os.ReadFile(exclude)
	if err != nil {
		t.Fatalf("read foreign exclude before child: %v", err)
	}

	extra := []string{
		"GIT_DIR=" + filepath.Join(repo, ".git"),
		"GIT_WORK_TREE=" + repo,
		testGitOverrideEnv + "=1",
	}
	out, err := runTMPDIRChild(t, outside, extra)
	if err != nil {
		t.Fatalf("git overrides made an outside TMPDIR look nested: %v\n%s", err, out)
	}
	if !strings.Contains(out, testTMPDIRChildRan) {
		t.Fatalf("positive-control child did not run:\n%s", out)
	}
	after, err := os.ReadFile(exclude)
	if err != nil {
		t.Fatalf("read foreign exclude after child: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("inherited git overrides let the child mutate a foreign exclude:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "init", "-q")
	cmd.Env = envWithoutGitRepositoryOverrides(os.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
}

func runTMPDIRChild(t *testing.T, tempDir string, extraEnv []string) (string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestTMPDIRGuardChild$")
	cmd.Dir = tempDir
	env := envWithoutGitRepositoryOverrides(os.Environ())
	env = append(env, "TMPDIR="+tempDir, testTMPDIRChildEnv+"=1")
	env = append(env, extraEnv...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestTMPDIRGuardChild(t *testing.T) {
	if os.Getenv(testTMPDIRChildEnv) == "" {
		t.Skip("helper process only")
	}
	if os.Getenv(testGitOverrideEnv) != "" {
		if got := ensureGitExcluded(context.Background(), ".config/mise/conf.d/kagikae.toml"); got != "" {
			t.Fatalf("outside cwd unexpectedly resolved a git exclude: %s", got)
		}
	}
	fmt.Fprintln(os.Stdout, testTMPDIRChildRan)
}

func TestRunnerGuardRefusesCredentialProgramsWithoutLeakingPayloads(t *testing.T) {
	const secretValue = "fixture-secret-must-not-appear"
	cases := []struct {
		name     string
		run      func()
		wantSeam string
		wantArg  string
	}{
		{
			name: "default",
			run: func() {
				runner.Run(context.Background(), "security", "add-generic-password", "-s", "svc", "-w", secretValue)
			},
			wantSeam: "runner.Default.Run",
			wantArg:  "add-generic-password",
		},
		{
			name: "default input",
			run: func() {
				runner.RunInput(context.Background(), secretValue, "secret-tool", "store", "key", "value")
			},
			wantSeam: "runner.Default.RunInput",
			wantArg:  "store",
		},
		{
			name: "interactive",
			run: func() {
				runner.RunInteractive(context.Background(), []string{"TOKEN=" + secretValue}, "claude", "login")
			},
			wantSeam: "runner.RunInteractive",
			wantArg:  "login",
		},
		{
			name: "with env",
			run: func() {
				runner.RunWithEnv(context.Background(), []string{"TOKEN=" + secretValue}, "gh", "api", "user")
			},
			wantSeam: "runner.RunWithEnv",
			wantArg:  "user",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := recoveredPanic(t, tc.run)
			if !strings.Contains(got, tc.wantSeam) || !strings.Contains(got, "argv=") ||
				!strings.Contains(got, tc.wantArg) {
				t.Fatalf("panic does not identify the seam and argv: %q", got)
			}
			if strings.Contains(got, secretValue) {
				t.Fatalf("panic leaked stdin, extraEnv, or a security -w value: %q", got)
			}
			if tc.name == "default" && !strings.Contains(got, "[redacted]") {
				t.Fatalf("security -w value was removed without a visible redaction marker: %q", got)
			}
		})
	}
}

func TestRunnerGuardDelegatesNonCredentialCommands(t *testing.T) {
	fake := &runnertest.Fake{Stdout: "ok", Code: 0}
	guard := cmdTestRunnerGuard{next: fake}
	out, _, code := guard.Run(context.Background(), "git", "rev-parse", "--show-toplevel")
	if out != "ok" || code != 0 || fake.Name != "git" {
		t.Fatalf("non-credential command was not delegated: out=%q code=%d call=%s %v", out, code, fake.Name, fake.Args)
	}

	guard.RunInput(context.Background(), "input", "cat")
	if fake.Name != "cat" || fake.Stdin != "input" {
		t.Fatalf("non-credential RunInput lost its invocation: call=%s %v stdin=%q", fake.Name, fake.Args, fake.Stdin)
	}
}

func recoveredPanic(t *testing.T, fn func()) (message string) {
	t.Helper()
	defer func() {
		value := recover()
		if value == nil {
			t.Fatal("call did not panic")
		}
		message = fmt.Sprint(value)
	}()
	fn()
	return ""
}
