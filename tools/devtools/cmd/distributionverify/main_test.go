package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/tools/devtools/commandrun"
	"github.com/webkaz-labs/kagikae/tools/devtools/distribution"
)

func TestSignedForeignCLI(t *testing.T) {
	packslip := os.Getenv("PACKSLIP_BIN")
	if packslip == "" {
		t.Skip("PACKSLIP_BIN required for real signing acceptance")
	}
	root := t.TempDir()
	goBinary, e := exec.LookPath("go")
	if e != nil {
		t.Fatal(e)
	}
	built := filepath.Join(root, "distributionverify")
	cwd, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	build, e := (commandrun.Command{Name: goBinary, Args: []string{"build", "-o", built, "."}, Env: os.Environ(), Dir: cwd, Timeout: time.Minute}).Run(context.Background())
	if e != nil || build.ExitCode != 0 {
		t.Fatal("build", e, build.Stderr)
	}
	env, e := distribution.IsolatedEnvironment(root)
	if e != nil {
		t.Fatal(e)
	}
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		t.Setenv(key, value)
	}
	shim := filepath.Join(root, "tools")
	if e = os.Mkdir(shim, 0o700); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(packslip, filepath.Join(shim, "packslip")); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", shim+":"+os.Getenv("PATH"))
	run := func(args ...string) {
		t.Helper()
		r, e := (commandrun.Command{Name: packslip, Args: args, Env: env, Dir: root, Timeout: time.Minute}).Run(context.Background())
		if e != nil || r.ExitCode != 0 {
			t.Fatalf("packslip %v: %v %d %s", args, e, r.ExitCode, r.Stderr)
		}
	}
	name := "side_1.2.3_darwin_arm64.tar.gz"
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	w := tar.NewWriter(z)
	for _, member := range []string{"bin/side", "NOTICE"} {
		data := []byte("notice")
		if member == "bin/side" {
			data = []byte("#!/bin/sh\nprintf 'side v1.2.3\\n'\n")
		}
		if e = w.WriteHeader(&tar.Header{Name: member, Size: int64(len(data)), Mode: 0o755, Typeflag: tar.TypeReg}); e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write(data); e != nil {
			t.Fatal(e)
		}
	}
	if e = errors.Join(w.Close(), z.Close()); e != nil {
		t.Fatal(e)
	}
	write := func(name string, data []byte) {
		t.Helper()
		if e := os.WriteFile(filepath.Join(root, name), data, 0o600); e != nil {
			t.Fatal(e)
		}
	}
	write(name, b.Bytes())
	digest := fmt.Sprintf("%x", sha256.Sum256(b.Bytes()))
	write("checksums.txt", []byte(digest+"  "+name+"\n"))
	key := filepath.Join(root, "fixture.key")
	run("keygen", "--out", key)
	run("create", "--project", "github.com/example/side", "--version", "1.2.3", "--source-repo", "https://github.com/example/side", "--tag", "v1.2.3", "--commit", strings.Repeat("a", 40), "--url-base", "https://github.com/example/side/releases/download/v1.2.3", "--bin", "bin/side", "--provenance", name+"=https://api.github.com/repos/example/side/attestations/sha256:"+digest, "--key", key, "--no-log", "--out", root, filepath.Join(root, name))
	spec := distribution.Spec{SchemaVersion: 1, Repository: "example/side", Project: "github.com/example/side", Tag: "v1.2.3", Version: "1.2.3", Commit: strings.Repeat("a", 40), Workflow: ".github/workflows/publish.yml", Identity: "https://github.com/example/side/.github/workflows/publish.yml@refs/tags/v1.2.3", Issuer: "https://token.actions.githubusercontent.com", Artifacts: []distribution.Artifact{{Name: name, OS: "darwin", Arch: "aarch64", Binary: "bin/side", Members: []string{"bin/side", "NOTICE"}}}, Resources: []map[string]string{}}
	raw, e := json.Marshal(spec)
	if e != nil {
		t.Fatal(e)
	}
	write("spec.json", raw)
	args := []string{"-spec", filepath.Join(root, "spec.json"), "-dir", root, "-bundle", filepath.Join(root, "packslip.sigstore.json"), "-fixture-key", filepath.Join(root, "fixture.pub"), "-native", name, "-expect", "side v1.2.3\n", "--", "version"}
	if e = check(context.Background(), args); e != nil {
		t.Fatal(e)
	}
	cliEnv := append([]string{}, env...)
	cliEnv[0] = "PATH=" + shim + ":/usr/bin:/bin"
	result, e := (commandrun.Command{Name: built, Args: args, Env: cliEnv, Dir: root, Timeout: time.Minute}).Run(context.Background())
	if e != nil || result.ExitCode != 0 {
		t.Fatal("built foreign cwd", e, result.Stderr, result.Stdout)
	}
	var report struct{ Status, Mode string }
	if e = json.Unmarshal([]byte(result.Stdout), &report); e != nil || report.Status != "success" || report.Mode != "synthetic fixture; build provenance unverified" {
		t.Fatal("report", e, result.Stdout)
	}
	write("install.sh", []byte("set -eu\nmkdir -p \"$4\"\ncurl --fail --location --silent --show-error --output \"$TMPDIR/asset.tar.gz\" \"https://github.com/$SIDE_REPO/releases/download/$2/side_1.2.3_darwin_arm64.tar.gz\"\ntar -xzf \"$TMPDIR/asset.tar.gz\" -C \"$TMPDIR\" bin/side\ncp \"$TMPDIR/bin/side\" \"$4/side\"\nchmod +x \"$4/side\"\n"))
	installArgs := append([]string{}, args[:8]...)
	installArgs = append(installArgs, "-installer", filepath.Join(root, "install.sh"), "-repository-env", "SIDE_REPO", "-installed-binary", "side", "-expect", "side v1.2.3\n", "--", "version")
	result, e = (commandrun.Command{Name: built, Args: installArgs, Env: cliEnv, Dir: root, Timeout: time.Minute}).Run(context.Background())
	if e != nil || result.ExitCode != 0 {
		t.Fatal("built installer", e, result.Stderr, result.Stdout)
	}
	run("keygen", "--out", filepath.Join(root, "other.key"))
	args[7] = filepath.Join(root, "other.pub")
	if e = check(context.Background(), args); e == nil {
		t.Fatal("wrong signing key accepted")
	}
}

func TestFixtureModeUsesParsedFlags(t *testing.T) {
	for _, args := range [][]string{{"--fixture-key", "/fixture.pub"}, {"--fixture-key=/fixture.pub"}, {"-fixture-key", "/fixture.pub"}} {
		mode, err := checkMode(context.Background(), args)
		if err == nil || mode != "synthetic fixture; build provenance unverified" {
			t.Fatalf("%v: %s %v", args, mode, err)
		}
	}
}
