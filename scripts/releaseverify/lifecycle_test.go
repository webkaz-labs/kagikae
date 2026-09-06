package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The helper child has a finite lifetime and acknowledges readiness over a socket.
// Its stdout/stderr are detached from the command's collection pipes deliberately.
func TestLifecycleHelper(t *testing.T) {
	mode := os.Getenv("RELEASEVERIFY_HELPER")
	if mode == "" {
		return
	}
	if mode == "child" {
		conn, err := net.DialTimeout("tcp", os.Getenv("RELEASEVERIFY_SOCKET"), time.Second)
		if err != nil {
			os.Exit(3)
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		fmt.Fprintln(conn, "ready")
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			fmt.Fprintln(conn, scanner.Text())
		}
		if scanner.Err() != nil {
			os.Exit(3)
		}
		return
	}
	if mode == "signal" {
		os.Args = []string{"releaseverify", testTag}
		main()
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestLifecycleHelper$")
	cmd.Env = append(os.Environ(), "RELEASEVERIFY_HELPER=child")
	if err := cmd.Start(); err != nil {
		os.Exit(3)
	}
	// The test only lets the parent exit after the descendant's socket is ready.
	conn, err := net.DialTimeout("tcp", os.Getenv("RELEASEVERIFY_PARENT"), time.Second)
	if err != nil {
		os.Exit(3)
	}
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	var b [1]byte
	_, _ = conn.Read(b[:])
	conn.Close()
	if mode == "wait" {
		_ = cmd.Wait()
	}
}

// expiredContext lets the fixture expire its deadline after readiness, without
// making machine startup speed part of the command cancellation assertion.
type expiredContext struct{ context.Context }

// Hide the embedded cancel context so derived contexts observe this context's
// DeadlineExceeded rather than subscribing directly to its Canceled parent.
func (ctx expiredContext) Value(any) any { return nil }

func (ctx expiredContext) Err() error {
	if ctx.Context.Err() != nil {
		return context.DeadlineExceeded
	}
	return nil
}

func TestExpiredContextPropagation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	derived, stop := context.WithTimeout(expiredContext{parent}, time.Minute)
	defer stop()
	cancel()
	select {
	case <-derived.Done():
		if derived.Err() != context.DeadlineExceeded {
			t.Fatalf("derived deadline error: %v", derived.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("deadline did not propagate")
	}
}

func TestCommandLifecycle(t *testing.T) {
	for _, mode := range []string{"normal", "timeout", "cancel", "SIGINT", "SIGTERM"} {
		t.Run(mode, func(t *testing.T) {
			childListener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer childListener.Close()
			parentListener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer parentListener.Close()
			root := t.TempDir()
			env := []string{"HOME=" + root, "XDG_CONFIG_HOME=" + filepath.Join(root, "config"), "XDG_DATA_HOME=" + filepath.Join(root, "data"), "XDG_STATE_HOME=" + filepath.Join(root, "state"), "XDG_RUNTIME_DIR=" + filepath.Join(root, "run"), "RELEASEVERIFY_SOCKET=" + childListener.Addr().String(), "RELEASEVERIFY_PARENT=" + parentListener.Addr().String()}
			helperMode := "wait"
			if mode == "normal" {
				helperMode = "normal"
			}
			if strings.HasPrefix(mode, "SIG") {
				helperMode = "signal"
			}
			env = append(env, "RELEASEVERIFY_HELPER="+helperMode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "timeout" {
				ctx = expiredContext{ctx}
			}
			done := make(chan error, 1)
			var outer *exec.Cmd
			var output bytes.Buffer
			work := filepath.Join(root, "work")
			calls := filepath.Join(root, "calls")
			if strings.HasPrefix(mode, "SIG") {
				if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o700); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"install.sh", "smoke-run.sh"} {
					if err := os.WriteFile(filepath.Join(root, "scripts", name), nil, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Mkdir(work, 0o700); err != nil {
					t.Fatal(err)
				}
				gh := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellQuote(calls) + "\nRELEASEVERIFY_HELPER=wait exec " + shellQuote(os.Args[0]) + " -test.run='^TestLifecycleHelper$'\n"
				if err := os.WriteFile(filepath.Join(root, "gh"), []byte(gh), 0o700); err != nil {
					t.Fatal(err)
				}
				outer = exec.Command(os.Args[0], "-test.run=^TestLifecycleHelper$")
				outer.Env = append(env, "TMPDIR="+work, "PATH="+root+":/usr/bin:/bin")
				outer.Dir = root
				outer.Stdout = &output
				outer.Stderr = &output
				if err := outer.Start(); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = outer.Process.Kill() }()
				go func() { done <- outer.Wait() }()
			} else {
				go func() {
					_, err := commandContext(ctx, os.Args[0], []string{"-test.run=^TestLifecycleHelper$"}, env, root)
					done <- err
				}()
			}
			_ = childListener.(*net.TCPListener).SetDeadline(time.Now().Add(5 * time.Second))
			conn, err := childListener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			reader := bufio.NewReader(conn)
			if line, err := reader.ReadString('\n'); err != nil || line != "ready\n" {
				t.Fatalf("ready: %q %v", line, err)
			}
			_ = parentListener.(*net.TCPListener).SetDeadline(time.Now().Add(5 * time.Second))
			parent, err := parentListener.Accept()
			if err != nil {
				t.Fatal(err)
			}
			_, _ = parent.Write([]byte{1})
			parent.Close()
			if mode == "cancel" || mode == "timeout" {
				cancel()
			}
			if outer != nil {
				sig := syscall.SIGINT
				if mode == "SIGTERM" {
					sig = syscall.SIGTERM
				}
				if err := outer.Process.Signal(sig); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				if mode == "normal" && err != nil {
					t.Fatal(err)
				}
				if (mode == "cancel" || mode == "timeout" || outer != nil) && err == nil {
					t.Fatal("expected cancellation failure")
				}
			case <-time.After(6 * time.Second):
				t.Fatal("command did not finish")
			}
			if outer != nil {
				var got result
				if err := json.Unmarshal(output.Bytes(), &got); err != nil || got.Status != "failed" || !strings.Contains(got.Reason, "gh failed") {
					t.Fatalf("main verdict %q: %v", output.String(), err)
				}
				entries, err := os.ReadDir(work)
				if err != nil || len(entries) != 0 {
					t.Fatalf("main left workdir: %v %v", entries, err)
				}
				data, err := os.ReadFile(calls)
				if err != nil || strings.Count(string(data), "\n") != 1 || !strings.HasPrefix(string(data), "release download "+testTag+" ") {
					t.Fatalf("unexpected stages %q %v", data, err)
				}
			}
			// A surviving child would echo this marker after the command returned.
			_, _ = conn.Write([]byte("after-return\n"))
			if line, err := reader.ReadString('\n'); err == nil || line != "" {
				t.Fatalf("descendant survived return: %q %v", line, err)
			} else if e, ok := err.(net.Error); ok && e.Timeout() {
				t.Fatal("child connection stayed open")
			}
		})
	}
}

func TestCommandFailures(t *testing.T) {
	root := t.TempDir()
	env := []string{"HOME=" + root}
	if _, err := commandContext(context.Background(), filepath.Join(root, "missing"), nil, env, root); err == nil {
		t.Fatal("missing command succeeded")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := commandContext(ctx, "/bin/echo", []string{"unexpected"}, env, root); err == nil {
		t.Fatal("canceled command succeeded")
	}
	out, err := commandContext(context.Background(), "/bin/echo", []string{"preserved"}, env, root)
	if err != nil || out != "preserved\n" {
		t.Fatalf("output %q error %v", out, err)
	}
}

func TestOwnedCleanup(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "owned")
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(nested, "outside")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(nested, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := removeWorkdir(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("owned root remains: %v", err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatalf("outside changed: %q %v", data, err)
	}
}

func TestUnrelatedProcessSurvives(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	root := t.TempDir()
	child := exec.Command(os.Args[0], "-test.run=^TestLifecycleHelper$")
	child.Env = []string{"HOME=" + root, "XDG_CONFIG_HOME=" + root, "XDG_DATA_HOME=" + root, "XDG_STATE_HOME=" + root, "XDG_RUNTIME_DIR=" + root, "RELEASEVERIFY_HELPER=child", "RELEASEVERIFY_SOCKET=" + listener.Addr().String()}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	_ = listener.(*net.TCPListener).SetDeadline(time.Now().Add(5 * time.Second))
	conn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	if _, err := commandContext(context.Background(), "/bin/echo", nil, child.Env, root); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(conn, "alive")
	if line, err := reader.ReadString('\n'); err != nil || line != "alive\n" {
		t.Fatalf("unrelated process killed: %q %v", line, err)
	}
}
