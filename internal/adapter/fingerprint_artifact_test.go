package adapter_test

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

// fingerprintArtifacts tells the audit how to get from the command selected by PATH
// to the installed artifact whose version the table records. Mirrors the artifact
// table in docs/VALIDATION.md § "Upstream Literal Fingerprints".
//
// Every resolver starts at exec.LookPath and resolves symlinks, then proves the
// selected command's real path contains the recorded version as a whole path
// component. That prevents an old retained artifact from passing after PATH moved to
// a newer build — the failure this check previously had for three tools.
//
// Retained old bundles still make a version bump cheap to investigate
// (.claude/skills/upstream-auth-drift/references/measuring.md calls the pair on disk
// the highest-yield moment); the audit simply does not select one by a remembered
// path anymore.
type fingerprintArtifact struct {
	binary            string
	commandIsArtifact bool
	requiredSiblings  []string
	sha256            string
	sha256Version     string
	copilotPackage    bool
}

var fingerprintArtifacts = map[string]fingerprintArtifact{
	constants.ToolClaude: {
		binary:            "claude",
		commandIsArtifact: true,
	},
	constants.ToolCursor: {
		binary:           "cursor-agent",
		requiredSiblings: []string{"node", "index.js"},
	},
	constants.ToolOpencode: {
		binary:            "opencode",
		commandIsArtifact: true,
	},
	// In the search list read out of copilot's launcher, ~/.copilot/pkg — the
	// path this map used to hold — is the **last** root, and on 2026-08-17 it held
	// nothing newer than 1.0.61 while 1.0.79 ran (docs/VALIDATION.md § Upstream
	// Behaviour Assumptions has the list as read). Reproduce the launcher's normal
	// local-package search and version ordering directly from that source; disabling
	// auto-update skips the newer-installed-package branch and can hide drift.
	constants.ToolCopilot: {
		binary:           "copilot",
		requiredSiblings: []string{"index.js", "app.js", filepath.Join("prebuilds", "darwin-arm64", "runtime.node")},
		copilotPackage:   true,
	},
	// Resolve the selected agy file rather than walking its install directory: the
	// previous build can sit beside it, and a directory walk would add that build's
	// literals to every count. agy may replace itself without renaming the containing
	// install directory. The shell which launched the first audit selected mise's
	// 1.1.23 artifact while
	// the surrounding interactive shell still selected 1.1.22. An artifact digest,
	// bound to its measured version, identifies the selected bytes without executing
	// either retained build and closes both stale-PATH and in-place-update cases.
	constants.ToolAgy: {
		binary:            "agy",
		commandIsArtifact: true,
		sha256:            "dea6443f3167d0ff1af9adf0bc9f96f13be85c8206a399bd33e2de87fdc39f7a",
		sha256Version:     "1.1.23",
	},
}

// artifactPath resolves the command selected by PATH and refuses to inspect it unless
// its real path proves it is the version recorded beside the counts. Cursor's command
// is one file in a relocatable package, so its sibling files are positive controls:
// a launcher-only install must fail before a directory walk can report plausible but
// incomplete counts.
func artifactPath(spec fingerprintArtifact, version string) (string, error) {
	if spec.binary == "" {
		return "", fmt.Errorf("no binary is recorded in fingerprintArtifacts")
	}
	if version == "" {
		return "", fmt.Errorf("no version recorded in the artifact table")
	}
	command, err := exec.LookPath(spec.binary)
	if err != nil {
		return "", fmt.Errorf("%s is not installed: %w", spec.binary, err)
	}
	realCommand, err := filepath.EvalSymlinks(command)
	if err != nil {
		return "", fmt.Errorf("resolve installed %s %s: %w", spec.binary, command, err)
	}
	path := realCommand
	if spec.copilotPackage {
		path, err = copilotPackageArtifact(realCommand, version, spec.requiredSiblings)
		if err != nil {
			return "", err
		}
	} else if spec.sha256 != "" {
		if spec.sha256Version != version {
			return "", fmt.Errorf("installed %s digest is recorded for version %s, not %s; "+
				"re-measure and update the version-to-digest pair together",
				spec.binary, spec.sha256Version, version)
		}
		if err := verifyArtifactSHA256(realCommand, spec.binary, version, spec.sha256); err != nil {
			return "", err
		}
	} else if !pathHasComponent(realCommand, version) {
		return "", fmt.Errorf("installed %s resolves to %s, which is not recorded version %s",
			spec.binary, realCommand, version)
	}

	if !spec.commandIsArtifact && !spec.copilotPackage {
		path = filepath.Dir(realCommand)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("not installed: %s — if the tool was upgraded, re-measure "+
			"the literals against the installed build and update both tables", path)
	}
	if spec.commandIsArtifact && !info.Mode().IsRegular() {
		return "", fmt.Errorf("incomplete %s payload: artifact %s is not a regular file",
			spec.binary, path)
	}
	if !spec.commandIsArtifact && !info.IsDir() {
		return "", fmt.Errorf("incomplete %s payload: %s is not a directory", spec.binary, path)
	}
	if err := validateRegularFiles(path, spec.requiredSiblings, false); err != nil {
		return "", fmt.Errorf("incomplete %s payload: %w", spec.binary, err)
	}
	return path, nil
}

func validateRegularFiles(root string, names []string, requireReadable bool) error {
	for _, name := range names {
		path := filepath.Join(root, name)
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("required sibling %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("required sibling %s is not a regular file", path)
		}
		if requireReadable && !isReadableRegularFile(path) {
			return fmt.Errorf("required sibling %s is not readable", path)
		}
	}
	return nil
}

func verifyArtifactSHA256(path, binary, version, want string) error {
	artifact, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open installed %s for content identity: %w", binary, err)
	}
	defer artifact.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, artifact); err != nil {
		return fmt.Errorf("hash installed %s for content identity: %w", binary, err)
	}
	got := fmt.Sprintf("%x", digest.Sum(nil))
	if got != want {
		return fmt.Errorf("installed %s SHA-256 is %s, not the digest recorded for version %s; "+
			"do not execute it to identify the bytes — preserve and re-measure the artifact pair",
			binary, got, version)
	}
	return nil
}

func copilotPackageArtifact(launcher, recordedVersion string, requiredSiblings []string) (string, error) {
	if override := os.Getenv("COPILOT_CLI_DIST_DIR"); override != "" {
		return "", fmt.Errorf("COPILOT_CLI_DIST_DIR=%s overrides copilot's package search; unset it before auditing", override)
	}
	launcherVersion, err := copilotLauncherVersion(launcher)
	if err != nil {
		return "", err
	}
	primaryRoot, searchRoots, err := copilotPackageRoots()
	if err != nil {
		return "", err
	}
	packages, err := installedCopilotPackages(searchRoots)
	if err != nil {
		return "", err
	}
	selected := ""
	for _, candidate := range packages {
		version := filepath.Base(candidate)
		if version == launcherVersion {
			continue
		}
		if compareCopilotVersions(version, launcherVersion) >= 0 {
			selected = candidate
		}
		break
	}
	if selected == "" {
		selected = filepath.Join(primaryRoot, "darwin-arm64", launcherVersion)
		builtInFiles := append(slices.Clone(requiredSiblings), ".extraction-complete")
		if err := validateRegularFiles(selected, builtInFiles, true); err != nil {
			return "", fmt.Errorf("copilot built-in package %s is not materialized: %w; the audit will not extract it",
				selected, err)
		}
	}
	selectedVersion := filepath.Base(selected)
	if selectedVersion != launcherVersion {
		for _, candidate := range packages {
			candidateVersion := filepath.Base(candidate)
			if candidate != selected && candidateVersion != launcherVersion &&
				compareCopilotVersions(candidateVersion, selectedVersion) == 0 {
				return "", fmt.Errorf("copilot package version %s has a version-order tie in multiple cache paths; "+
					"the launcher's locale-dependent path tie-break cannot be reproduced safely",
					selectedVersion)
			}
		}
	}
	if selectedVersion != recordedVersion {
		return "", fmt.Errorf("copilot launcher selected package version %s, not recorded version %s",
			selectedVersion, recordedVersion)
	}
	return selected, nil
}

var copilotLauncherVersionPattern = regexp.MustCompile(`COPILOT_CLI_BINARY_VERSION="([0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)"`)

func copilotLauncherVersion(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read copilot launcher source: %w", err)
	}
	matches := copilotLauncherVersionPattern.FindAllSubmatch(data, -1)
	if len(matches) != 1 {
		return "", fmt.Errorf("copilot launcher has %d embedded binary-version assignments, want 1", len(matches))
	}
	return string(matches[0][1]), nil
}

func copilotPackageRoots() (string, []string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", nil, err
	}
	xdgCache := os.Getenv("XDG_CACHE_HOME")
	if xdgCache == "" {
		xdgCache = filepath.Join(home, ".cache")
	}
	platformCache := filepath.Join(home, "Library", "Caches", "copilot")
	candidates := []string{
		os.Getenv("COPILOT_PKG_CACHE_HOME"),
		os.Getenv("COPILOT_CACHE_HOME"),
		platformCache,
		filepath.Join(xdgCache, "copilot"),
		os.Getenv("COPILOT_HOME"),
		filepath.Join(home, ".copilot"),
	}
	primary := os.Getenv("COPILOT_PKG_CACHE_HOME")
	if primary == "" {
		primary = os.Getenv("COPILOT_CACHE_HOME")
	}
	if primary == "" {
		primary = platformCache
	}
	seen := map[string]bool{}
	roots := []string{}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		absolute, err := filepath.Abs(candidate)
		if err != nil {
			return "", nil, fmt.Errorf("resolve copilot package cache %s: %w", candidate, err)
		}
		if seen[absolute] {
			continue
		}
		seen[absolute] = true
		roots = append(roots, filepath.Join(absolute, "pkg"))
	}
	primaryAbsolute, err := filepath.Abs(primary)
	if err != nil {
		return "", nil, fmt.Errorf("resolve primary copilot package cache %s: %w", primary, err)
	}
	return filepath.Join(primaryAbsolute, "pkg"), roots, nil
}

func installedCopilotPackages(packageRoots []string) ([]string, error) {
	packages := []string{}
	for _, root := range packageRoots {
		for _, arch := range []string{"universal", "darwin-arm64"} {
			archRoot := filepath.Join(root, arch)
			entries, err := os.ReadDir(archRoot)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("read copilot package root %s: %w", archRoot, err)
			}
			for _, entry := range entries {
				candidate := filepath.Join(archRoot, entry.Name())
				if isReadableRegularFile(filepath.Join(candidate, "index.js")) {
					packages = append(packages, candidate)
				}
			}
		}
	}
	sort.Slice(packages, func(i, j int) bool {
		left, right := filepath.Base(packages[i]), filepath.Base(packages[j])
		if comparison := compareCopilotVersions(left, right); comparison != 0 {
			return comparison > 0
		}
		return packages[i] < packages[j]
	})
	return packages, nil
}

func isReadableRegularFile(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	info, err := file.Stat()
	return err == nil && info.Mode().IsRegular()
}

var (
	copilotCoreVersionPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)`)
	copilotSemverPattern      = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([^+]+))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)
	copilotIdentifierPattern  = regexp.MustCompile(`^[0-9A-Za-z-]+$`)
	copilotNumericPattern     = regexp.MustCompile(`^\d+$`)
)

func compareCopilotVersions(left, right string) int {
	leftCore, leftOK := copilotCoreVersion(left)
	rightCore, rightOK := copilotCoreVersion(right)
	if !leftOK && !rightOK {
		return 0
	}
	if !leftOK {
		return -1
	}
	if !rightOK {
		return 1
	}
	for i := range leftCore {
		if leftCore[i] != rightCore[i] {
			return leftCore[i] - rightCore[i]
		}
	}
	leftPre, leftSemver := copilotPrerelease(left)
	rightPre, rightSemver := copilotPrerelease(right)
	if leftSemver && rightSemver {
		if len(leftPre) == 0 || len(rightPre) == 0 {
			if len(leftPre) == len(rightPre) {
				return 0
			}
			if len(leftPre) == 0 {
				return 1
			}
			return -1
		}
		return compareCopilotPrerelease(leftPre, rightPre)
	}
	leftHasDash, rightHasDash := strings.Contains(left, "-"), strings.Contains(right, "-")
	if leftHasDash != rightHasDash {
		if leftHasDash {
			return -1
		}
		return 1
	}
	return strings.Compare(left, right)
}

func copilotCoreVersion(version string) ([3]int, bool) {
	match := copilotCoreVersionPattern.FindStringSubmatch(version)
	if match == nil {
		return [3]int{}, false
	}
	var result [3]int
	for i := range result {
		value, err := strconv.Atoi(match[i+1])
		if err != nil {
			return [3]int{}, false
		}
		result[i] = value
	}
	return result, true
}

func copilotPrerelease(version string) ([]string, bool) {
	match := copilotSemverPattern.FindStringSubmatch(version)
	if match == nil {
		return nil, false
	}
	if match[4] == "" {
		return []string{}, true
	}
	identifiers := strings.Split(match[4], ".")
	for _, identifier := range identifiers {
		if !copilotIdentifierPattern.MatchString(identifier) ||
			(copilotNumericPattern.MatchString(identifier) && len(identifier) > 1 && identifier[0] == '0') {
			return nil, false
		}
	}
	return identifiers, true
}

func compareCopilotPrerelease(left, right []string) int {
	for i := 0; i < min(len(left), len(right)); i++ {
		if left[i] == right[i] {
			continue
		}
		leftNumeric := copilotNumericPattern.MatchString(left[i])
		rightNumeric := copilotNumericPattern.MatchString(right[i])
		if leftNumeric != rightNumeric {
			if leftNumeric {
				return -1
			}
			return 1
		}
		if leftNumeric && len(left[i]) != len(right[i]) {
			return len(left[i]) - len(right[i])
		}
		return strings.Compare(left[i], right[i])
	}
	return len(left) - len(right)
}

func pathHasComponent(path, component string) bool {
	for path != filepath.Dir(path) {
		if filepath.Base(path) == component {
			return true
		}
		path = filepath.Dir(path)
	}
	return filepath.Base(path) == component
}

func TestArtifactPathFollowsSelectedCommandAndChecksCursorPayload(t *testing.T) {
	root := t.TempDir()
	version := "2026.09.02-c22c1a3"
	payload := filepath.Join(root, "cursor-cli", version, "dist-package")
	if err := os.MkdirAll(payload, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cursor-agent", "node", "index.js"} {
		if err := os.WriteFile(filepath.Join(payload, name), []byte(name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	binDir := filepath.Join(root, "bin")
	if err := os.Mkdir(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(payload, "cursor-agent"), filepath.Join(binDir, "cursor-agent")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	spec := fingerprintArtifact{
		binary:           "cursor-agent",
		requiredSiblings: []string{"node", "index.js"},
	}
	got, err := artifactPath(spec, version)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(payload)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("artifactPath = %q, want selected command's payload %q", got, want)
	}
	if _, err := artifactPath(spec, "2026.09.01-old"); err == nil || !strings.Contains(err.Error(), "not recorded version") {
		t.Fatalf("wrong recorded version error = %v", err)
	}
	if err := os.Remove(filepath.Join(payload, "node")); err != nil {
		t.Fatal(err)
	}
	if _, err := artifactPath(spec, version); err == nil || !strings.Contains(err.Error(), "required sibling") {
		t.Fatalf("incomplete payload error = %v", err)
	}
}

func TestArtifactPathVerifiesAgyBytesWithoutExecutingThem(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "antigravity-cli", "stale-directory-name")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(binDir, "agy")
	marker := binary + ".executed"
	contents := []byte("#!/bin/sh\n/usr/bin/touch \"$0.executed\"\nprintf \"1.1.23\\n\"\n")
	if err := os.WriteFile(binary, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	spec := fingerprintArtifact{
		binary:            "agy",
		commandIsArtifact: true,
		sha256:            "8a29a4c396899749a7e4f521217fd312573735f7cbfb2b949073b9395adc02e9",
		sha256Version:     "1.1.22",
	}
	want, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := artifactPath(spec, "1.1.22"); err != nil {
		t.Fatal(err)
	} else if got != want {
		t.Fatalf("artifactPath = %q, want %q", got, want)
	}
	if _, err := artifactPath(spec, "1.1.23"); err == nil || !strings.Contains(err.Error(), "version-to-digest pair") {
		t.Fatalf("changed version with stale digest error = %v", err)
	}
	if err := os.WriteFile(binary, append(contents, '!'), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := artifactPath(spec, "1.1.22"); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("changed artifact digest error = %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("selected agy executable ran during byte identity check: marker stat = %v", err)
	}
}

func TestArtifactPathRejectsRetainedCopilotPackageTheLauncherSelects(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(binDir, "copilot")
	marker := launcher + ".executed"
	launcherSource := fmt.Sprintf("#!/bin/sh\n/usr/bin/touch %q\nexit 99\n// process.env.COPILOT_CLI_BINARY_VERSION=\"1.0.82\";\n", marker)
	if err := os.WriteFile(launcher, []byte(launcherSource), 0o700); err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(root, "package-cache")
	newerRoot := filepath.Join(root, "newer-package-cache")
	writePackage := func(packageRoot, version string) {
		t.Helper()
		path := filepath.Join(packageRoot, "pkg", "darwin-arm64", version)
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		for name, contents := range map[string]string{
			"index.js": version,
			"app.js":   version,
			filepath.Join("prebuilds", "darwin-arm64", "runtime.node"): version,
		} {
			file := filepath.Join(path, name)
			if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, version := range []string{"1.0.79", "1.0.82"} {
		writePackage(cacheRoot, version)
	}
	writePackage(newerRoot, "1.0.83")
	t.Setenv("PATH", binDir)
	t.Setenv("COPILOT_PKG_CACHE_HOME", cacheRoot)
	t.Setenv("COPILOT_CACHE_HOME", newerRoot)
	// The old resolver set this value and therefore took Ir()'s branch which skips
	// lh(), hiding the installed 1.0.83. The audit models normal local selection
	// directly, so even an inherited false value cannot restore that false green.
	t.Setenv("COPILOT_AUTO_UPDATE", "false")
	spec := fingerprintArtifact{
		binary:           "copilot",
		requiredSiblings: []string{"index.js", "app.js", filepath.Join("prebuilds", "darwin-arm64", "runtime.node")},
		copilotPackage:   true,
	}
	t.Setenv("COPILOT_CLI_DIST_DIR", filepath.Join(root, "dist-override"))
	if _, err := artifactPath(spec, "1.0.82"); err == nil || !strings.Contains(err.Error(), "overrides copilot's package search") {
		t.Fatalf("distribution override error = %v", err)
	}
	t.Setenv("COPILOT_CLI_DIST_DIR", "")
	if _, err := artifactPath(spec, "1.0.82"); err == nil || !strings.Contains(err.Error(), "selected package version 1.0.83") {
		t.Fatalf("retained selected package error = %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("selected copilot launcher ran during source inspection: marker stat = %v", err)
	}

	got, err := artifactPath(spec, "1.0.83")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(newerRoot, "pkg", "darwin-arm64", "1.0.83")
	if got != want {
		t.Fatalf("artifactPath = %q, want selected package %q", got, want)
	}
	if err := os.RemoveAll(want); err != nil {
		t.Fatal(err)
	}
	if _, err := artifactPath(spec, "1.0.82"); err == nil ||
		!strings.Contains(err.Error(), "not materialized") || !strings.Contains(err.Error(), ".extraction-complete") {
		t.Fatalf("built-in package without extraction marker error = %v", err)
	}
	builtInMarker := filepath.Join(cacheRoot, "pkg", "darwin-arm64", "1.0.82", ".extraction-complete")
	if err := os.WriteFile(builtInMarker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = artifactPath(spec, "1.0.82")
	if err != nil {
		t.Fatal(err)
	}
	want = filepath.Join(cacheRoot, "pkg", "darwin-arm64", "1.0.82")
	if got != want {
		t.Fatalf("artifactPath = %q, want built-in package %q", got, want)
	}
}

func TestCompareCopilotVersionsMatchesLauncherPredicate(t *testing.T) {
	tests := []struct {
		left, right string
		want        int
	}{
		{"1.0.83", "1.0.82", 1},
		{"1.0.82", "1.0.82", 0},
		{"1.0.81", "1.0.82", -1},
		{"1.0.82", "1.0.82-rc.1", 1},
		{"1.0.82-rc.2", "1.0.82-rc.10", -1},
		{"1.0.82-alpha", "1.0.82-1", 1},
		{"not-a-version", "1.0.82", -1},
	}
	for _, test := range tests {
		t.Run(test.left+"_"+test.right, func(t *testing.T) {
			got := compareCopilotVersions(test.left, test.right)
			if got < 0 {
				got = -1
			} else if got > 0 {
				got = 1
			}
			if got != test.want {
				t.Fatalf("compareCopilotVersions(%q, %q) = %d, want sign %d", test.left, test.right, got, test.want)
			}
		})
	}
}
