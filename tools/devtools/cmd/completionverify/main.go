// Command completionverify checks trusted shell adapters against explicit cases.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/webkaz-labs/kagikae/tools/devtools/completioncheck"
)

func check(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: completionverify /absolute/path/spec.json")
	}
	raw, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	if len(raw) > 1024*1024 {
		return errors.New("spec too large")
	}
	var s completioncheck.CandidateSpec
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(&s); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing spec data")
	}
	return completioncheck.Candidates(ctx, s)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := check(ctx, os.Args[1:])
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(`{"schema_version":1,"status":"success","shells":["bash","zsh"],"zsh_observation":"compadd capture; registration checked separately","fish":"unverified"}`)
}
