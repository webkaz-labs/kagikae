// Command namingagreement observes the production credential write argv without
// executing a subprocess. The upstream half lives in verify.py. This observer
// exercises the adapter/artifact boundary; it does not test CLI orchestration.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

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

func main() {
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
