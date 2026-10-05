// Command releaseverify verifies published artifacts before executing the native
// binary. gh owns download/provenance; the installer receives verified bytes via
// a non-forwarding curl fixture, so its HTTP transport is not tested. Run alone:
// smoke-run checks checkout status and info/exclude for leaks.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/webkaz-labs/kagikae/tools/devtools/commandrun"
	"github.com/webkaz-labs/kagikae/tools/devtools/distribution"
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
		Toolchain     *tools   `json:"toolchain,omitempty"`
		Consumer      string   `json:"consumer,omitempty"`
		Reason        string   `json:"reason,omitempty"`
	}
)

// tools records the versions this run actually exercised. Verifier versions
// float within their accepted range, so the result is the compatibility record.
type tools struct {
	Packslip string `json:"packslip"`
	Mise     string `json:"mise"`
}

// exitError is a completed process with a nonzero status, distinguishable from
// a launch failure so a refusal control cannot pass on a missing executable.
// stderr lets a control require the refusal reason; Error() omits it.
type exitError struct {
	name, stderr string
	code         int
}

func (e exitError) Error() string { return fmt.Sprintf("%s failed: exit status %d", e.name, e.code) }

func command(name string, args, env []string, cwd string) (string, error) {
	return commandContext(context.Background(), name, args, env, cwd)
}

func commandContext(parent context.Context, name string, args, env []string, cwd string) (string, error) {
	result, err := (commandrun.Command{Name: name, Args: args, Env: env, Dir: cwd, Timeout: 5 * time.Minute}).Run(parent)
	if err != nil {
		return "", fmt.Errorf("%s failed: %w", filepath.Base(name), err)
	}
	if result.ExitCode != 0 {
		return "", exitError{name: filepath.Base(name), stderr: result.Stderr, code: result.ExitCode}
	}
	return result.Stdout, nil
}

// intelMacDropped is the first release without a darwin/amd64 (Intel macOS)
// archive; earlier tags keep the four archives they were published with. It
// assumes the release after v0.23.0 is v0.24.0; adjust it before tagging if not.
var intelMacDropped = [3]uint64{0, 24, 0}

// releaseFrom reports whether an explicit vX.Y.Z tag is at or after from.
func releaseFrom(tag string, from [3]uint64) bool {
	if !tagPattern.MatchString(tag) {
		return false
	}
	got, ok := distribution.Triple(tag[1:])
	return ok && slices.Compare(got[:], from[:]) >= 0
}

func archiveName(tag, system, arch string) string {
	return fmt.Sprintf("kae_%s_%s_%s.tar.gz", tag[1:], system, arch)
}

func archivesFor(tag string) ([]string, error) {
	if !tagPattern.MatchString(tag) {
		return nil, errors.New("expected explicit vX.Y.Z tag")
	}
	var names []string
	for _, system := range []string{"darwin", "linux"} {
		for _, arch := range []string{"amd64", "arm64"} {
			if system == "darwin" && arch == "amd64" && releaseFrom(tag, intelMacDropped) {
				continue
			}
			names = append(names, archiveName(tag, system, arch))
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
	selected := ""
	if binary {
		selected = "kae"
	}
	return distribution.ReadArchive(path, members, selected)
}

func verifyArchives(dir string, names []string) error {
	artifacts := make([]distribution.Artifact, 0, len(names))
	for _, name := range names {
		parts := strings.Split(name, "_")
		members := []string{"kae", "LICENSE", "README.md"}
		if len(parts) == 4 && packslipRelease("v"+parts[1]) {
			for _, shell := range []string{"bash", "zsh", "fish"} {
				members = append(members, "completions/kae."+shell)
			}
		}
		artifacts = append(artifacts, distribution.Artifact{Name: name, Members: members})
	}
	return distribution.VerifyArchives(dir, artifacts)
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

func installerSmoke(repo, dir, tag, native string, run commandFunc) error {
	base := "https://github.com/" + repository + "/releases/download/" + tag + "/"
	files := map[string]string{}
	for _, name := range []string{native, "checksums.txt"} {
		files[base+name] = filepath.Join(dir, name)
	}
	shim, err := distribution.CurlFixture(files)
	if err != nil {
		return err
	}
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
	native := archiveName(tag, system, arch)
	if !slices.Contains(names, native) {
		return result{}, errors.New("native platform unavailable")
	}
	var toolchain *tools
	if packslipRelease(tag) {
		if toolchain, err = verifierTools(repo, run); err != nil {
			return result{}, err
		}
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
		if err := refuseForeignSigner(filepath.Join(dir, packslipAsset), tag, commit, dir, repo, run); err != nil {
			return result{}, err
		}
		packslip = "signature, source, archives and completion resources verified; foreign signer identity refused"
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
		doc := heading + "\n\n```bash\ngo run ./scripts/packslipverify published " + tag + "\n```\n"
		if err := runSmokeDocument(repo, dir, filepath.Join(dir, "consumer.md"), heading, doc, run); err != nil {
			return result{}, fmt.Errorf("published mise consumer: %w", err)
		}
		consumer = "native mise backend and completion verified"
		if os.Getenv("KAE_RELEASE_VERIFY_FRESH") == "1" {
			consumer += "; explicit isolated zero-age exception"
		}
	}
	return result{Status: "success", Tag: tag, Archives: names, NativeVersion: strings.TrimSpace(version), Installer: "verified-assets fixture", Packslip: packslip, Toolchain: toolchain, Consumer: consumer}, nil
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
	names, err := archivesFor(tag)
	if err != nil {
		return result{Status: "failed", Reason: err.Error()}, 1
	}
	if !slices.Contains(names, archiveName(tag, runtime.GOOS, runtime.GOARCH)) {
		return result{Status: "unavailable", Reason: "native platform unavailable"}, 2
	}
	requiredTools := []string{"gh", "bash", "git"}
	if packslipRelease(tag) {
		requiredTools = append(requiredTools, "packslip", "mise", "go")
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
