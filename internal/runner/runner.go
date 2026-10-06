// Package runner is the test seam for every subprocess kagikae executes
// (security, secret-tool, binary detection probes). Production code never
// calls exec.Command directly.
package runner

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Runner interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr string, code int)
	// RunInput is Run with data piped to stdin. Used for commands that must
	// not receive secrets via argv (e.g. secret-tool store).
	RunInput(ctx context.Context, stdin string, name string, args ...string) (stdout, stderr string, code int)
}

type OSRunner struct{}

var Default Runner = OSRunner{}

func Run(ctx context.Context, name string, args ...string) (string, string, int) {
	return Default.Run(ctx, name, args...)
}

func RunInput(ctx context.Context, stdin, name string, args ...string) (string, string, int) {
	return Default.RunInput(ctx, stdin, name, args...)
}

// RunInteractive runs a command with inherited stdio for login flows and
// kae run children. extraEnv entries (KEY=VALUE) are appended to the current
// environment. Overridable in tests.
var RunInteractive = func(ctx context.Context, extraEnv []string, name string, args ...string) (int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), nil
	}
	return 1, err
}

// RunWithEnv is Run with extra KEY=VALUE entries appended to the environment,
// for probes that need a specific credential present (e.g. resolving a token's
// live login by running a CLI with the token in its env var). It captures
// stdout/stderr like Run; pass a nil extraEnv to run against the ambient
// environment. Overridable in tests.
var RunWithEnv = func(ctx context.Context, extraEnv []string, name string, args ...string) (string, string, int) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	err := cmd.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return stdout.String(), stderr.String(), exitErr.ExitCode()
	}
	return stdout.String(), err.Error(), 1
}

// Launcher is the optional seam for a program kae starts and does not read.
// Launch returns its exit code, or an error when it could not be started. Its
// output is not captured: a captured pipe is inherited by anything the program
// leaves running (a file manager an opener starts), and Wait would not return
// until that exits.
type Launcher interface {
	Launch(ctx context.Context, name string, args ...string) (int, error)
}

// Launch runs name through Default's Launch. A Default without one — the
// Runner test doubles — answers through Run, which records the call; the
// output is dropped.
func Launch(ctx context.Context, name string, args ...string) (int, error) {
	if l, ok := Default.(Launcher); ok {
		return l.Launch(ctx, name, args...)
	}
	_, _, code := Default.Run(ctx, name, args...)
	return code, nil
}

// Launch passes stdin and stdout as the null device and stderr as kae's own,
// files rather than pipes, so it waits for the program itself only.
func (OSRunner) Launch(ctx context.Context, name string, args ...string) (int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = os.Stderr // Stdin and Stdout nil: the null device
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), nil
	}
	return 1, err
}

// launchWaitDelay bounds how long LaunchWithEnv waits, once ctx is done and the
// program is killed, for it to be reaped.
const launchWaitDelay = time.Second

// LaunchWithEnv is Launch for a program kae starts with extra KEY=VALUE entries
// appended to its environment (the last entry for a key wins, as os/exec
// documents), and whose output nobody reads: stdin, stdout and stderr are all
// the null device. Like Launch it uses files rather than pipes, because a pipe
// is inherited by whatever the program leaves running — a daemon that a
// lifecycle command (codex app-server daemon restart) starts — and Wait would
// not return until that exits, past any deadline on ctx. WaitDelay is a
// backstop on the wait after ctx is done; with no pipe there is nothing left for
// it to bound, so no test can show it working (a mutation removing it survives),
// and this comment stands in for one. It returns the exit code, or an error when the
// program could not be started or reaped. Overridable in tests.
var LaunchWithEnv = func(ctx context.Context, extraEnv []string, name string, args ...string) (int, error) {
	cmd := exec.CommandContext(ctx, name, args...) // Stdin, Stdout, Stderr nil: the null device
	cmd.WaitDelay = launchWaitDelay
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), nil
	}
	return 1, err
}

// Snippet truncates subprocess stderr for safe inclusion in diagnostics.
// It must never be applied to stdout of credential reads, which is secret.
func Snippet(stderr string) string {
	s := strings.TrimSpace(stderr)
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

// With replaces the process-wide runner for a single test. Do not use it from
// tests that call t.Parallel; inject Runner directly when parallelism matters.
func With(r Runner, fn func()) {
	saved := Default
	Default = r
	defer func() { Default = saved }()
	fn()
}

func (OSRunner) Run(ctx context.Context, name string, args ...string) (string, string, int) {
	return run(ctx, nil, name, args...)
}

func (OSRunner) RunInput(ctx context.Context, stdin, name string, args ...string) (string, string, int) {
	return run(ctx, strings.NewReader(stdin), name, args...)
}

func run(ctx context.Context, stdin *strings.Reader, name string, args ...string) (string, string, int) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return stdout.String(), stderr.String(), exitErr.ExitCode()
	}
	return stdout.String(), err.Error(), 1
}
