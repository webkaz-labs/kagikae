package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/tools/devtools/distribution"
)

const runnerTemp = "${{ runner.temp }}"

// signStep is the release workflow's Packslip step as written: the `uses:` pin,
// its line index and trailing comment, and the `with:` inputs (literal `|`
// block scalars joined by newline).
type signStep struct {
	uses, comment string
	line          int
	with          map[string]string
}

// workflowRun is one single-line `run:` command and its line index.
type workflowRun struct {
	line int
	text string
}

// signInputs is every Action input the release workflow sets. The tests below
// check each one: TestSignerCLIStatesSpecLibc reproduces artifacts, manifest,
// project, bin, resources and attest's provenance links in its create call;
// upload and out decide only where the bundle goes, so the always-on test pins
// their values. A new input changes what is signed and must join both.
var signInputs = []string{"artifacts", "attest", "bin", "manifest", "out", "project", "resources", "upload"}

// checkSignInputs requires exactly signInputs, with attest linking the release
// job's attestations and upload left to --packslip-publish.
func checkSignInputs(t *testing.T, step signStep) {
	t.Helper()
	keys := make([]string, 0, len(step.with))
	for k := range step.with {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if !slices.Equal(keys, signInputs) {
		t.Fatalf("Packslip step inputs %v, want exactly %v", keys, signInputs)
	}
	if step.with["attest"] != "link" || step.with["upload"] != "false" {
		t.Fatalf("Packslip step needs attest: link and upload: false; got attest %q, upload %q", step.with["attest"], step.with["upload"])
	}
}

// readReleaseWorkflow returns the Packslip step and every single-line `run:` of
// the release workflow. It reads only the shapes release.yml uses, so a layout
// it cannot read fails the test rather than passing it.
func readReleaseWorkflow(t *testing.T) (signStep, []workflowRun) {
	t.Helper()
	data, err := os.ReadFile("../../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(data), "\n")
	indent := func(line string) int { return len(line) - len(strings.TrimLeft(line, " ")) }
	var step signStep
	var runs []workflowRun
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if run, ok := strings.CutPrefix(trimmed, "run: "); ok {
			runs = append(runs, workflowRun{i, run})
		}
		uses, ok := strings.CutPrefix(trimmed, "uses: jdx/packslip@")
		if !ok {
			continue
		}
		if step.uses != "" {
			t.Fatal("release workflow has more than one Packslip step")
		}
		step.uses, step.comment, _ = strings.Cut(uses, " # ")
		step.line = i
		step.with = map[string]string{}
		i++
		if i >= len(lines) || strings.TrimSpace(lines[i]) != "with:" {
			t.Fatal("Packslip step has no with: block right after uses:")
		}
		base := indent(lines[i])
		key, keyIndent, block := "", 0, false
		for i+1 < len(lines) {
			line := lines[i+1]
			text := strings.TrimSpace(line)
			if text != "" && indent(line) <= base {
				break
			}
			i++
			switch {
			case text == "":
			case block && indent(line) > keyIndent:
				step.with[key] += text + "\n"
			case strings.HasPrefix(text, "#"):
			default:
				k, v, found := strings.Cut(text, ":")
				if !found {
					t.Fatalf("unreadable with: line %q", line)
				}
				key, keyIndent = k, indent(line)
				v = strings.TrimSpace(v)
				if comment := strings.Index(v, " #"); comment >= 0 {
					v = strings.TrimSpace(v[:comment])
				}
				block = v == "|"
				switch {
				case block:
					v = ""
				case strings.HasPrefix(v, "|") || strings.HasPrefix(v, ">"):
					t.Fatalf("with: %s uses block scalar %q; this reader keeps only a literal |", key, v)
				case strings.HasPrefix(v, `"`) || strings.HasPrefix(v, "'"):
					t.Fatalf("with: %s is quoted (%s); write it plain so this reader sees the value YAML gives the Action", key, v)
				}
				step.with[key] = v
			}
		}
	}
	if step.uses == "" {
		t.Fatal("release workflow has no Packslip step")
	}
	return step, runs
}

// TestReleaseWorkflowMatchesVerifier ties the signing step to the paths and
// signer version the Go side assumes; drift would otherwise fail only in the tag job.
func TestReleaseWorkflowMatchesVerifier(t *testing.T) {
	step, runs := readReleaseWorkflow(t)
	const staging = "$RUNNER_TEMP/kae-published"
	prepare := `--packslip-prepare "$RELEASE_TAG" "$RELEASE_COMMIT" "` + staging + `"`
	publish := `--packslip-publish "$RELEASE_TAG" "$RELEASE_COMMIT" "` + staging + `" "$BUNDLE"`
	lineOf := func(want string) int {
		i := slices.IndexFunc(runs, func(run workflowRun) bool { return strings.HasSuffix(run.text, want) })
		if i < 0 {
			t.Fatalf("release workflow does not run releaseverify %s", want)
		}
		return runs[i].line
	}
	// The Action signs what prepare staged, and publish checks what it signed.
	if p, q := lineOf(prepare), lineOf(publish); p >= step.line || step.line >= q {
		t.Fatalf("step order: prepare line %d, Packslip line %d, publish line %d", p+1, step.line+1, q+1)
	}
	checkSignInputs(t, step)
	dir := runnerTemp + "/kae-published"
	if got := step.with["manifest"]; got != dir+"/"+packslipManifestFile {
		t.Fatalf("manifest input %q is not the file --packslip-prepare writes", got)
	}
	glob, ok := strings.CutPrefix(step.with["artifacts"], dir+"/")
	if !ok || strings.ContainsAny(glob, "/ ") {
		t.Fatalf("artifacts input %q is not one glob in the staging directory", step.with["artifacts"])
	}
	names, _ := archivesFor("v0.24.0")
	for _, name := range append(names, packslipManifestFile, "checksums.txt") {
		matched, err := filepath.Match(glob, name)
		if err != nil || matched != strings.HasSuffix(name, ".tar.gz") {
			t.Fatalf("artifacts glob %q on %s: matched=%v err=%v", glob, name, matched, err)
		}
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(step.uses) {
		t.Fatalf("Packslip Action is not pinned to a full commit: %q", step.uses)
	}
	fixture, err := os.ReadFile("../packslipverify/fixtures.go")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`signerPackslip = "([0-9.]+)"`).FindSubmatch(fixture)
	if m == nil || !strings.HasPrefix(step.comment, "v"+string(m[1])+";") {
		t.Fatalf("Action pin comment %q does not name the fixture signer %q", step.comment, m)
	}
}

// TestPreparePackslipWritesManifestAfterSource requires the manifest only once
// the archives and their build provenance verified, and never otherwise.
func TestPreparePackslipWritesManifestAfterSource(t *testing.T) {
	for _, provenance := range []bool{true, false} {
		dir := filepath.Join(t.TempDir(), "kae-published")
		err := preparePackslip(signedTag, signedCommit, dir, dir, func(name string, args, env []string, cwd string) (string, error) {
			switch {
			case name == "gh" && args[0] == "release":
				fixtureForTag(t, dir, signedTag, "")
			case name == "gh" && args[0] == "attestation" && !provenance:
				return "", errors.New("provenance refused")
			case name == "gh" && args[0] == "attestation":
			default:
				t.Fatalf("unexpected command %s %v", name, args)
			}
			return "", nil
		})
		_, statErr := os.Stat(filepath.Join(dir, packslipManifestFile))
		if provenance && (err != nil || statErr != nil) {
			t.Fatalf("verified prepare: %v, manifest: %v", err, statErr)
		}
		if !provenance && (err == nil || !errors.Is(statErr, os.ErrNotExist)) {
			t.Fatalf("refused provenance: %v, manifest: %v", err, statErr)
		}
	}
}

// TestSignerCLIStatesSpecLibc signs static Linux and CGO-free darwin archives
// with a real Packslip CLI the way the release workflow's Action does (the
// signInputs read from release.yml, key signing in place of OIDC) and checks
// the statement against releaseSpec. Without the manifest, a signer from 1.4.0
// must lose libc, which is what the manifest exists to prevent.
func TestSignerCLIStatesSpecLibc(t *testing.T) {
	packslip := os.Getenv("PACKSLIP_BIN")
	if packslip == "" {
		t.Skip("PACKSLIP_BIN required for real signing acceptance")
	}
	out, err := exec.Command(packslip, "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	version, _ := strings.CutPrefix(strings.TrimSpace(string(out)), "packslip ")
	got, ok := distribution.Triple(version)
	if !ok {
		t.Fatalf("unrecognized packslip version %q", out)
	}
	infersLibc := slices.Compare(got[:], []uint64{1, 4, 0}) >= 0
	step, _ := readReleaseWorkflow(t)
	checkSignInputs(t, step)
	const tag, commit = "v0.24.0", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	archives := staticArchives(t, tag)
	for _, withManifest := range []bool{true, false} {
		root := t.TempDir()
		resolve := func(v string) string { return strings.ReplaceAll(v, runnerTemp, root) }
		dir := resolve(runnerTemp + "/kae-published")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		for name, data := range archives {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if withManifest {
			if err := writePackslipManifest(tag, commit, dir); err != nil {
				t.Fatal(err)
			}
		}
		key := filepath.Join(root, "fixture.key")
		if out, err := exec.Command(packslip, "keygen", "--out", key).CombinedOutput(); err != nil {
			t.Fatalf("keygen: %v %s", err, out)
		}
		// The Action's create invocation, with its defaults for a tag push.
		base := "https://github.com/" + repository
		args := []string{
			"create", "--project", step.with["project"], "--version", strings.TrimPrefix(tag, "v"), "--out", filepath.Join(root, "out"),
			"--url-base", base + "/releases/download/" + tag, "--notes-url", base + "/releases/tag/" + tag, "--source-repo", base, "--tag", tag, "--commit", commit,
		}
		for _, b := range strings.Fields(step.with["bin"]) {
			args = append(args, "--bin", b)
		}
		if withManifest {
			args = append(args, "--manifest", resolve(step.with["manifest"]))
		}
		for _, r := range strings.Split(step.with["resources"], "\n") {
			if r != "" {
				args = append(args, "--resource", r)
			}
		}
		files, err := filepath.Glob(resolve(step.with["artifacts"]))
		if err != nil || len(files) != len(archives) {
			t.Fatalf("artifacts glob: %v %v", files, err)
		}
		for _, f := range files {
			args = append(args, "--provenance", fmt.Sprintf("%s=https://api.github.com/repos/%s/attestations/sha256:%x", filepath.Base(f), repository, sha256.Sum256(archives[filepath.Base(f)])))
		}
		args = append(args, "--key", key, "--no-log")
		if out, err := exec.Command(packslip, append(args, files...)...).CombinedOutput(); err != nil {
			t.Fatalf("create (manifest=%v): %v %s", withManifest, err, out)
		}
		raw, err := exec.Command(packslip, "show", "--raw", filepath.Join(root, "out", packslipAsset)).Output()
		if err != nil {
			t.Fatal(err)
		}
		err = validatePackslip(raw, tag, commit, dir)
		t.Logf("packslip %s manifest=%v: %v", version, withManifest, err)
		switch {
		case withManifest && err != nil:
			t.Fatalf("manifest-signed statement refused: %v", err)
		case !withManifest && infersLibc && (err == nil || !strings.Contains(err.Error(), "mismatch: libc")):
			t.Fatalf("packslip %s without the manifest: want a libc mismatch, got %v", version, err)
		case !withManifest && !infersLibc && err != nil:
			t.Fatalf("packslip %s without the manifest should default to gnu: %v", version, err)
		}
	}
}

// staticArchives builds a CGO_ENABLED=0 stub per release platform (static on
// Linux, CGO-free darwin binaries still link libSystem) and packs it with the
// release's archive members, so Packslip reads a static ELF on Linux.
func staticArchives(t *testing.T, tag string) map[string][]byte {
	t.Helper()
	src := t.TempDir()
	for name, body := range map[string]string{"go.mod": "module stub\n\ngo 1.21\n", "main.go": "package main\n\nfunc main() { println(\"kae " + tag + "\") }\n"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	names, _ := archivesFor(tag)
	archives := map[string][]byte{}
	for _, name := range names {
		parts := strings.Split(strings.TrimSuffix(name, ".tar.gz"), "_")
		binary := filepath.Join(src, parts[2]+"-"+parts[3])
		build := exec.Command("go", "build", "-o", binary, ".")
		build.Dir = src
		build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+parts[2], "GOARCH="+parts[3], "GOFLAGS=", "GOWORK=off")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v %s", name, err, out)
		}
		exe, err := os.ReadFile(binary)
		if err != nil {
			t.Fatal(err)
		}
		members := map[string][]byte{"kae": exe, "LICENSE": []byte("license\n"), "README.md": []byte("readme\n")}
		for _, shell := range []string{"bash", "zsh", "fish"} {
			members["completions/kae."+shell] = []byte("# " + shell + "\n")
		}
		order := make([]string, 0, len(members))
		for member := range members {
			order = append(order, member)
		}
		sort.Strings(order)
		var buf bytes.Buffer
		z := gzip.NewWriter(&buf)
		w := tar.NewWriter(z)
		for _, member := range order {
			mode := int64(0o644)
			if member == "kae" {
				mode = 0o755
			}
			if err := w.WriteHeader(&tar.Header{Name: member, Mode: mode, Size: int64(len(members[member])), Typeflag: tar.TypeReg}); err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write(members[member]); err != nil {
				t.Fatal(err)
			}
		}
		if err := errors.Join(w.Close(), z.Close()); err != nil {
			t.Fatal(err)
		}
		archives[name] = buf.Bytes()
	}
	return archives
}
