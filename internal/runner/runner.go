// Package runner is the test seam for every subprocess kagikae executes
// (security, secret-tool, binary detection probes). Production code never
// calls exec.Command directly.
package runner

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
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
	setExtraEnv(cmd, extraEnv)
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
	setExtraEnv(cmd, extraEnv)
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
	return launchResult(cmd.Run())
}

// ErrStillRunning is LaunchWithEnv's error when ctx ended before the program
// exited: kae stopped waiting and left the program running.
var ErrStillRunning = errors.New("still running")

// LaunchWithEnv is Launch for a program kae starts with extra KEY=VALUE entries
// appended to its environment (the last entry for a key wins, as os/exec
// documents) and whose report on stdout kae reads once it has exited. stdin and
// stderr are the null device and stdout an unlinked temporary file, as
// QueryWithEnv's, so a daemon the program leaves running holds no pipe of kae's
// and kae waits for the program only. The program runs in a session of its own,
// so the interrupt and hangup of kae's terminal do not reach it, and it is never
// killed: when ctx ends first, LaunchWithEnv stops waiting and returns -1 and
// ErrStillRunning, leaving the program running past kae's own exit, and reads
// none of its output. A program that has exited by then is reported as exited,
// with at most QueryMaxOutput bytes of its stdout (empty when they cannot be
// read). A ctx already ended starts nothing: 1 and ctx's error, as does a
// temporary file that cannot be made. Overridable in tests.
var LaunchWithEnv = func(ctx context.Context, extraEnv []string, name string, args ...string) (stdout string, code int, err error) {
	if err := ctx.Err(); err != nil {
		return "", 1, err
	}
	out, err := os.CreateTemp("", "kae-launch-*")
	if err != nil {
		return "", 1, err
	}
	// The program has its own descriptor of the file; kae's is closed on return,
	// also when the program is left running.
	defer func() { _ = out.Close() }()
	if err := os.Remove(out.Name()); err != nil {
		return "", 1, err
	}
	cmd := exec.Command(name, args...) // Stdin, Stderr nil: the null device
	cmd.Stdout = out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	setExtraEnv(cmd, extraEnv)
	if err := cmd.Start(); err != nil {
		return "", 1, err
	}
	// The goroutine reaps the program whenever it exits, also after kae stopped
	// waiting, so a kae that lives on leaves no zombie.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	exited := func(err error) (string, int, error) {
		code, err := launchResult(err)
		if err != nil {
			return "", code, err
		}
		data, _ := readQueryOutput(out)
		return data, code, nil
	}
	select {
	case err := <-done:
		return exited(err)
	case <-ctx.Done():
		select { // a program that exited as ctx ended counts as exited
		case err := <-done:
			return exited(err)
		default:
			return "", -1, ErrStillRunning
		}
	}
}

// setExtraEnv appends extraEnv to the inherited environment of cmd; the last
// entry for a key wins, as os/exec documents. A nil extraEnv leaves cmd on the
// ambient environment.
func setExtraEnv(cmd *exec.Cmd, extraEnv []string) {
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
}

// QueryMaxOutput caps how much of a QueryWithEnv or LaunchWithEnv program's
// stdout is read.
const QueryMaxOutput = 1 << 20

// QueryWithEnv runs a program kae reads the stdout of and that may leave a
// process running holding its stdio (a daemon a status command starts), with
// extra KEY=VALUE entries appended to the environment as LaunchWithEnv does.
// stdout is an unlinked temporary file rather than a pipe, and stdin and stderr
// the null device, so kae waits for the program only: a pipe would keep Wait
// open until whatever inherited it exits, and closing kae's end early would
// leave that process writing into a broken pipe. At most QueryMaxOutput bytes
// are read, from the start of the file without moving the offset the process
// shares with kae (readQueryOutput); what the program and anything it leaves
// running write is not capped, only kae's read of it. code is the exit status, -1 when ctx killed the program, and 1 when
// it could not be started or its output could not be read. Overridable in tests.
var QueryWithEnv = func(ctx context.Context, extraEnv []string, name string, args ...string) (stdout string, code int) {
	out, err := os.CreateTemp("", "kae-query-*")
	if err != nil {
		return "", 1
	}
	defer func() { _ = out.Close() }()
	// Unlinked at once: the open descriptors keep it readable, and nothing is left
	// on disk whatever the program leaves running.
	if err := os.Remove(out.Name()); err != nil {
		return "", 1
	}
	cmd := exec.CommandContext(ctx, name, args...) // Stdin, Stderr nil: the null device
	cmd.Stdout = out
	setExtraEnv(cmd, extraEnv)
	code, err = launchResult(cmd.Run())
	if err != nil {
		return "", 1
	}
	data, err := readQueryOutput(out)
	if err != nil {
		return "", 1
	}
	return data, code
}

// readQueryOutput reads up to QueryMaxOutput bytes of f from its start with
// positioned reads. It never seeks: a process QueryWithEnv's program left running
// shares f's offset and may still be writing, and moving the offset back would
// make its next write land over the start of the output.
func readQueryOutput(f *os.File) (string, error) {
	data, err := io.ReadAll(io.NewSectionReader(f, 0, QueryMaxOutput))
	return string(data), err
}

// launchResult is the (exit code, error) of a launched program from its Run
// error: 0 on success, the exit code when it exited non-zero, or an error when
// it could not be started.
func launchResult(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	if exitErr := (*exec.ExitError)(nil); errors.As(err, &exitErr) {
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
