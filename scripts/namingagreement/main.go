// Command namingagreement observes the production credential write argv without
// executing a subprocess. Explicit verify runs the reviewed upstream comparison. The observer
// exercises the adapter/artifact boundary; it does not test CLI orchestration.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/adapter/claude"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/keychain"
	"github.com/webkaz-labs/kagikae/internal/runner"
)

type observer struct{ writes [][]string }

func (o *observer) Run(_ context.Context, name string, args ...string) (string, string, int) {
	if name != "security" || len(args) == 0 {
		return "", "unexpected subprocess refused", 1
	}
	switch args[0] {
	case "find-generic-password":
		return "", keychain.NotFoundMarker, 44
	case "add-generic-password":
		clean := append([]string(nil), args...)
		for i := range clean {
			if i > 0 && clean[i-1] == "-w" {
				clean[i] = "<redacted>"
			}
		}
		o.writes = append(o.writes, clean)
		return "", "", 0
	default:
		return "", "unexpected security operation refused", 1
	}
}

func (o *observer) RunInput(context.Context, string, string, ...string) (string, string, int) {
	return "", "stdin subprocess refused", 1
}

func observe(env adapter.Env) ([]string, error) {
	o := &observer{}
	// Install the non-forwarding runner before resolving or applying artifacts.
	previous := runner.Default
	runner.Default = o
	defer func() { runner.Default = previous }()
	specs, err := (claude.Claude{}).Artifacts(context.Background(), env)
	if err != nil {
		return nil, err
	}
	for _, sp := range specs {
		if sp.Kind == constants.KindKeychain {
			if err := artifact.ApplyLive(context.Background(), sp, artifact.Value{Present: true, Data: []byte(`{"claudeAiOauth":{}}`)}); err != nil {
				return nil, err
			}
		}
	}
	if len(o.writes) != 1 {
		return nil, fmt.Errorf("expected one credential write, observed %d", len(o.writes))
	}
	return o.writes[0], nil
}

func observeMain() {
	env := adapter.Env{GOOS: "darwin", Home: os.Getenv("HOME"), Getenv: os.Getenv, LookupEnv: os.LookupEnv, Username: "main"}
	args, err := observe(env)
	if err == nil {
		err = json.NewEncoder(os.Stdout).Encode(args)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func main() {
	if filepath.Base(os.Args[0]) == "security" {
		os.Exit(securityMode(os.Args[1:]))
	}
	if len(os.Args) == 2 && os.Args[1] == "observe" {
		observeMain()
		return
	}
	if len(os.Args) != 2 || os.Args[1] != "verify" {
		fmt.Fprintln(os.Stderr, "usage: namingagreement verify | observe")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	repo, err := os.Getwd()
	if err == nil {
		var self string
		self, err = os.Executable()
		if err == nil {
			err = verify(ctx, repo, self, runCommand, os.Stdout)
		}
	}
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "naming agreement failed: "+err.Error())
		os.Exit(1)
	}
}
