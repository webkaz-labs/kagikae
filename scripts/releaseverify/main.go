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
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
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
		Reason        string   `json:"reason,omitempty"`
	}
)

func command(name string, args, env []string, cwd string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	c.WaitDelay = 5 * time.Second
	c.Dir = cwd
	if env != nil {
		c.Env = env
	}
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
		if seen[h.Name] || (h.Name != "kae" && h.Name != "LICENSE" && h.Name != "README.md") || h.Typeflag != tar.TypeReg {
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
	if len(seen) != 3 {
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
	if err := os.WriteFile(docPath, []byte(doc), 0o600); err != nil {
		return err
	}
	env := []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "SMOKE_DOC=") && !strings.HasPrefix(entry, "SMOKE_WHOLE_FILE=") {
			env = append(env, entry)
		}
	}
	env = append(env, "SMOKE_DOC="+docPath, "SMOKE_WHOLE_FILE=0")
	_, err := run("bash", []string{"scripts/smoke-run.sh", "## Published installer"}, env, repo)
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
	for _, name := range names {
		if _, err := run("gh", []string{"attestation", "verify", filepath.Join(dir, name), "--repo", repository}, nil, repo); err != nil {
			return result{}, err
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
	return result{Status: "success", Tag: tag, Archives: names, NativeVersion: strings.TrimSpace(version), Installer: "verified-assets fixture"}, nil
}

func mainResult() (result, int) {
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
	for _, tool := range []string{"gh", "bash", "git"} {
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
	defer os.RemoveAll(dir)
	got, err := verify(tag, repo, dir, runtime.GOOS, runtime.GOARCH, command)
	if err != nil {
		return result{Status: "failed", Tag: tag, Reason: err.Error()}, 1
	}
	return got, 0
}

func main() {
	got, code := mainResult()
	if err := json.NewEncoder(os.Stdout).Encode(got); err != nil {
		os.Exit(1)
	}
	os.Exit(code)
}
