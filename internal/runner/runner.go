// Package runner is the test seam for every subprocess kagikae executes
// (security, secret-tool, binary detection probes). Production code never
// calls exec.Command directly.
package runner

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
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
