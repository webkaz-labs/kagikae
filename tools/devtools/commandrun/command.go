// Package commandrun bounds maintainer subprocesses and owns their process groups.
// Detached descendants remain outside this cleanup boundary.
package commandrun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type Command struct {
	Name       string
	Args, Env  []string
	Dir, Stdin string
	Timeout    time.Duration
}

type Result struct {
	Stdout, Stderr string
	ExitCode       int
}

// Run separates an ordinary nonzero exit from launch, timeout and cleanup errors.
// Env nil inherits the parent environment; an empty slice inherits nothing.
func (spec Command) Run(parent context.Context) (result Result, runErr error) {
	if spec.Timeout <= 0 {
		return result, errors.New("command timeout must be positive")
	}
	ctx, cancel := context.WithTimeout(parent, spec.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, spec.Name, spec.Args...)
	cmd.WaitDelay = 5 * time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stop := func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.Cancel = stop
	cmd.Dir, cmd.Env = spec.Dir, spec.Env
	cmd.Stdin = strings.NewReader(spec.Stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	defer func() {
		if cmd.Process != nil {
			if err := stop(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				runErr = errors.Join(runErr, fmt.Errorf("%s process group cleanup failed: %w", filepath.Base(spec.Name), err))
			}
		}
	}()
	err := cmd.Run()
	result.Stdout, result.Stderr = stdout.String(), stderr.String()
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		result.ExitCode = exit.ExitCode()
		return result, nil
	}
	return result, err
}
