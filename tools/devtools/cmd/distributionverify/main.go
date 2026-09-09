// Command distributionverify checks staged releases using a trusted product spec.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/webkaz-labs/kagikae/tools/devtools/commandrun"
	"github.com/webkaz-labs/kagikae/tools/devtools/distribution"
)

func check(ctx context.Context, args []string) error { _, err := checkMode(ctx, args); return err }
func checkMode(ctx context.Context, args []string) (mode string, err error) {
	mode = "unresolved"
	f := flag.NewFlagSet("distributionverify", flag.ContinueOnError)
	specPath := f.String("spec", "", "trusted JSON product specification")
	dir := f.String("dir", "", "staged release directory")
	bundle := f.String("bundle", "", "signed Packslip bundle")
	fixtureKey := f.String("fixture-key", "", "explicit synthetic fixture public key; provenance unverified")
	installer := f.String("installer", "", "trusted conventional shell installer (optional)")
	repositoryEnv := f.String("repository-env", "", "installer repository environment variable")
	installedBinary := f.String("installed-binary", "", "installed binary name")
	native := f.String("native", "", "verified artifact to execute (optional)")
	expected := f.String("expect", "", "exact native stdout, including newline")
	if err := f.Parse(args); err != nil {
		return mode, err
	}
	mode = "published"
	if *fixtureKey != "" {
		mode = "synthetic fixture; build provenance unverified"
	}
	if !filepath.IsAbs(*specPath) || !filepath.IsAbs(*dir) || !filepath.IsAbs(*bundle) {
		return mode, errors.New("spec, dir and bundle require absolute paths")
	}
	if *native == "" && *installer == "" && f.NArg() != 0 {
		return mode, errors.New("native arguments require -native")
	}
	raw, err := os.ReadFile(*specPath)
	if err != nil {
		return mode, err
	}
	spec, err := distribution.ParseSpec(raw)
	if err != nil {
		return mode, err
	}
	run := func(name string, args, env []string, cwd string) (string, error) {
		r, err := (commandrun.Command{Name: name, Args: args, Env: env, Dir: cwd, Timeout: 5 * time.Minute}).Run(ctx)
		if err != nil {
			return "", err
		}
		if r.ExitCode != 0 {
			return "", fmt.Errorf("%s exit %d", name, r.ExitCode)
		}
		return r.Stdout, nil
	}
	var verified distribution.Verified
	if *fixtureKey != "" {
		verified, err = distribution.VerifyFixture(spec, *dir, *bundle, *dir, *fixtureKey, run)
	} else {
		verified, err = distribution.Verify(spec, *dir, *bundle, *dir, run)
	}
	if err != nil {
		return mode, err
	}
	if *installer != "" {
		if err := verified.RunInstaller(ctx, distribution.Installer{Path: *installer, RepositoryEnv: *repositoryEnv, Binary: *installedBinary, VersionArgs: f.Args(), Expected: *expected}); err != nil {
			return mode, err
		}
	}
	if *native != "" {
		return mode, verified.RunNative(ctx, *native, f.Args(), *expected)
	}
	return mode, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	mode, err := checkMode(ctx, os.Args[1:])
	stop()
	result := map[string]any{"schema_version": 1, "status": "success", "mode": mode}
	if err != nil {
		result["status"] = "failed"
		result["reason"] = err.Error()
	}
	if e := json.NewEncoder(os.Stdout).Encode(result); e != nil {
		os.Exit(1)
	}
	if err != nil {
		os.Exit(1)
	}
}
