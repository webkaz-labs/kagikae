// Command releaseverify verifies published artifacts before executing the native
// binary. gh owns download/provenance; the installer receives verified bytes via
// a non-forwarding curl fixture, so its HTTP transport is not tested. Run alone:
// smoke-run checks checkout status and info/exclude for leaks.
package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"
)

const repository = "webkaz-labs/kagikae"

var (
	tagPattern      = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	checksumPattern = regexp.MustCompile(`^([0-9a-fA-F]{64})  ([^\s]+)$`)
)

type (
	commandFunc func(string, []string, []string, string) (string, error)
	result      struct {
		Status        string   `json:"status"`
		Tag           string   `json:"tag,omitempty"`
		Archives      []string `json:"archives,omitempty"`
		NativeVersion string   `json:"native_version,omitempty"`
		Installer     string   `json:"installer,omitempty"`
		Packslip      string   `json:"packslip,omitempty"`
		Consumer      string   `json:"consumer,omitempty"`
		Reason        string   `json:"reason,omitempty"`
	}
)

func command(name string, args, env []string, cwd string) (string, error) {
	return commandContext(context.Background(), name, args, env, cwd)
}

// commandContext owns only the new process group. Detached descendants are outside
// this boundary; stop the group even when its original parent exits successfully.
func commandContext(parent context.Context, name string, args, env []string, cwd string) (output string, commandErr error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	c.WaitDelay = 5 * time.Second
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stop := func() error {
		err := syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	c.Cancel = stop
	c.Dir = cwd
	if env != nil {
		c.Env = env
	}
	// Output owns the pipe collection; cleanup must also run after a successful Wait.
	defer func() {
		if c.Process != nil {
			if err := stop(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				commandErr = errors.Join(commandErr, fmt.Errorf("%s process group cleanup failed: %w", filepath.Base(name), err))
			}
		}
	}()
	out, err := c.Output()
	if err != nil {
		return "", fmt.Errorf("%s failed: %w", filepath.Base(name), err)
	}
	return string(out), nil
}

func archivesFor(tag string) ([]string, error) {
	if !tagPattern.MatchString(tag) {
		return nil, errors.New("expected explicit vX.Y.Z tag")
	}
	var names []string
	for _, system := range []string{"darwin", "linux"} {
		for _, arch := range []string{"amd64", "arm64"} {
			names = append(names, fmt.Sprintf("kae_%s_%s_%s.tar.gz", tag[1:], system, arch))
		}
	}
	sort.Strings(names)
	return names, nil
}

func readArchive(path string, binary bool) ([]byte, error) {
	modern := false
	parts := strings.Split(filepath.Base(path), "_")
	if len(parts) == 4 {
		modern = packslipRelease("v" + parts[1])
	}
	members := map[string]bool{"kae": true, "LICENSE": true, "README.md": true}
	if modern {
		for _, shell := range []string{"bash", "zsh", "fish"} {
			members["completions/kae."+shell] = true
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer z.Close()
	t := tar.NewReader(z)
	seen := map[string]bool{}
	var payload []byte
	for {
		h, e := t.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		if seen[h.Name] || !members[h.Name] || h.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("unexpected, duplicate or nonregular archive member: %s", h.Name)
		}
		seen[h.Name] = true
		if h.Name == "kae" && binary {
			payload, err = io.ReadAll(t)
			if err != nil {
				return nil, err
			}
		}
	}
	if len(seen) != len(members) {
		return nil, errors.New("missing archive member")
	}
	// Consume the gzip trailer too, so truncated/compressed corruption fails.
	if _, err = io.Copy(io.Discard, z); err != nil {
		return nil, err
	}
	return payload, nil
}

func verifyArchives(dir string, names []string) error {
	data, err := os.ReadFile(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		return err
	}
	manifest := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		m := checksumPattern.FindStringSubmatch(line)
		if m == nil || manifest[m[2]] != "" {
			return errors.New("malformed or duplicate checksum entry")
		}
		manifest[m[2]] = strings.ToLower(m[1])
	}
	if len(manifest) != len(names) {
		return errors.New("unexpected checksum manifest set")
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.tar.gz"))
	if err != nil {
		return err
	}
	if len(files) != len(names) {
		return errors.New("unexpected downloaded archive set")
	}
	for _, name := range names {
		if manifest[name] == "" {
			return errors.New("missing checksum entry")
		}
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("archive is not a regular file")
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if hex.EncodeToString(h.Sum(nil)) != manifest[name] {
			return fmt.Errorf("checksum mismatch: %s", name)
		}
		if _, err := readArchive(path, false); err != nil {
			return err
		}
	}
	return nil
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

func installerSmoke(repo, dir, tag, native string, run commandFunc) error {
	base := "https://github.com/" + repository + "/releases/download/" + tag + "/"
	shim := "#!/bin/sh\nset -eu\n[ \"$#\" -eq 7 ] || exit 2\n[ \"$1\" = --fail ] && [ \"$2\" = --location ] && [ \"$3\" = --silent ] && [ \"$4\" = --show-error ] && [ \"$5\" = --output ] || exit 2\ncase \"$7\" in\n"
	for _, name := range []string{native, "checksums.txt"} {
		shim += shellQuote(base+name) + ") cp " + shellQuote(filepath.Join(dir, name)) + " \"$6\" ;;\n"
	}
	shim += "*) exit 2 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "curl"), []byte(shim), 0o700); err != nil {
		return err
	}
	doc := "## Published installer\n\n```bash\nPATH=" + shellQuote(dir) + ":\"$PATH\" KAE_REPO=" + repository + " sh scripts/install.sh --version " + tag + " --install-dir \"$HOME/bin\"\ntest \"$(\"$HOME/bin/kae\" version)\" = 'kae " + tag + "'\n```\n"
	docPath := filepath.Join(dir, "installer.md")
	return runSmokeDocument(repo, dir, docPath, "## Published installer", doc, run)
}

func runSmokeDocument(repo, dir, docPath, heading, doc string, run commandFunc) error {
	if err := os.WriteFile(docPath, []byte(doc), 0o600); err != nil {
		return err
	}
	env := []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "SMOKE_DOC=") && !strings.HasPrefix(entry, "SMOKE_WHOLE_FILE=") && !strings.HasPrefix(entry, "TMPDIR=") {
			env = append(env, entry)
		}
	}
	env = append(env, "SMOKE_DOC="+docPath, "SMOKE_WHOLE_FILE=0", "TMPDIR="+dir)
	_, err := run("bash", []string{"scripts/smoke-run.sh", heading}, env, repo)
	return err
}

func verify(tag, repo, dir, system, arch string, run commandFunc) (result, error) {
	names, err := archivesFor(tag)
	if err != nil {
		return result{}, err
	}
	native := fmt.Sprintf("kae_%s_%s_%s.tar.gz", tag[1:], system, arch)
	if (system != "darwin" && system != "linux") || (arch != "amd64" && arch != "arm64") {
		return result{}, errors.New("native platform unavailable")
	}
	if _, err := run("gh", []string{"release", "download", tag, "--repo", repository, "--dir", dir, "--pattern", "checksums.txt", "--pattern", "kae_*.tar.gz"}, nil, repo); err != nil {
		return result{}, err
	}
	if err := verifyArchives(dir, names); err != nil {
		return result{}, err
	}
	packslip := ""
	if packslipRelease(tag) {
		commit, err := run("git", []string{"rev-parse", tag + "^{commit}"}, nil, repo)
		commit = strings.TrimSpace(commit)
		if err != nil || !commitPattern.MatchString(commit) {
			return result{}, errors.New("fetch the explicit release tag before verification")
		}
		if err := verifySource(tag, commit, dir, names, repo, run); err != nil {
			return result{}, err
		}
		if _, err := run("gh", []string{"release", "download", tag, "--repo", repository, "--dir", dir, "--pattern", packslipAsset}, nil, repo); err != nil {
			return result{}, err
		}
		if err := verifyPackslip(filepath.Join(dir, packslipAsset), tag, commit, dir, repo, run); err != nil {
			return result{}, err
		}
		packslip = "signature, source, archives and completion resources verified"
	} else {
		for _, name := range names {
			if _, err := run("gh", []string{"attestation", "verify", filepath.Join(dir, name), "--repo", repository}, nil, repo); err != nil {
				return result{}, err
			}
		}
	}
	payload, err := readArchive(filepath.Join(dir, native), true)
	if err != nil {
		return result{}, err
	}
	binary := filepath.Join(dir, "kae")
	if err := os.WriteFile(binary, payload, 0o700); err != nil {
		return result{}, err
	}
	// Reuse the canonical root derivation with no inherited auth/tool variables.
	// The outer temporary directory owns the preamble's nested HOME as well.
	version, err := run("/bin/sh", []string{"-eu", "-c", `. scripts/smoke-env.sh; "$1" version`, "sh", binary},
		[]string{"PATH=/usr/bin:/bin", "TMPDIR=" + dir}, repo)
	if err != nil {
		return result{}, err
	}
	if strings.TrimSpace(version) != "kae "+tag {
		return result{}, errors.New("native version mismatch")
	}
	if err := installerSmoke(repo, dir, tag, native, run); err != nil {
		return result{}, err
	}
	consumer := ""
	if packslipRelease(tag) {
		heading := "## Published Packslip consumer"
		doc := heading + "\n\n```bash\npython3 -B scripts/packslipverify/published.py " + tag + "\n```\n"
		if err := runSmokeDocument(repo, dir, filepath.Join(dir, "consumer.md"), heading, doc, run); err != nil {
			return result{}, fmt.Errorf("published mise consumer: %w", err)
		}
		consumer = "native mise backend and completion verified"
		if os.Getenv("KAE_RELEASE_VERIFY_FRESH") == "1" {
			consumer += "; explicit isolated zero-age exception"
		}
	}
	return result{Status: "success", Tag: tag, Archives: names, NativeVersion: strings.TrimSpace(version), Installer: "verified-assets fixture", Packslip: packslip, Consumer: consumer}, nil
}

// removeWorkdir restores owner access to directories a killed smoke may have
// made read-only. WalkDir does not follow symlinks outside this owned root.
func removeWorkdir(root string) error {
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.Chmod(path, 0o700)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return os.RemoveAll(root)
}

func mainResult(ctx context.Context) (got result, code int) {
	if len(os.Args) > 1 && strings.HasPrefix(os.Args[1], "--packslip-") {
		return packslipJob(ctx, os.Args[1:])
	}
	if len(os.Args) != 2 {
		return result{Status: "failed", Reason: "usage: releaseverify vX.Y.Z (from repository root)"}, 1
	}
	tag := os.Args[1]
	if _, err := archivesFor(tag); err != nil {
		return result{Status: "failed", Reason: err.Error()}, 1
	}
	if (runtime.GOOS != "darwin" && runtime.GOOS != "linux") || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return result{Status: "unavailable", Reason: "native platform unavailable"}, 2
	}
	requiredTools := []string{"gh", "bash", "git"}
	if packslipRelease(tag) {
		requiredTools = append(requiredTools, "packslip", "mise", "python3")
	}
	for _, tool := range requiredTools {
		if _, err := exec.LookPath(tool); err != nil {
			return result{Status: "unavailable", Reason: "required tool unavailable: " + tool}, 2
		}
	}
	repo, err := os.Getwd()
	if err != nil {
		return result{Status: "failed", Reason: err.Error()}, 1
	}
	for _, path := range []string{"scripts/install.sh", "scripts/smoke-run.sh"} {
		if _, err := os.Stat(filepath.Join(repo, path)); err != nil {
			return result{Status: "unavailable", Reason: "run from repository root"}, 2
		}
	}
	dir, err := os.MkdirTemp("", "kae-release-verify-")
	if err != nil {
		return result{Status: "failed", Reason: err.Error()}, 1
	}
	defer func() {
		if err := removeWorkdir(dir); err != nil {
			got = result{Status: "failed", Tag: tag, Reason: "temporary directory cleanup failed: " + err.Error()}
			code = 1
		}
	}()
	run := func(name string, args, env []string, cwd string) (string, error) {
		return commandContext(ctx, name, args, env, cwd)
	}
	got, err = verify(tag, repo, dir, runtime.GOOS, runtime.GOARCH, run)
	if err != nil {
		return result{Status: "failed", Tag: tag, Reason: err.Error()}, 1
	}
	return got, 0
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	got, code := mainResult(ctx)
	stop()
	if err := json.NewEncoder(os.Stdout).Encode(got); err != nil {
		os.Exit(1)
	}
	os.Exit(code)
}
