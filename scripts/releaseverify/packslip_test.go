package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	signedTag    = "v0.21.0"
	signedCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func signedStatement(t *testing.T, dir string) map[string]any {
	t.Helper()
	names := fixtureForTag(t, dir, signedTag, "")
	subjects, artifacts := []any{}, []any{}
	base := "https://github.com/" + repository
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		subjects = append(subjects, map[string]any{"name": name, "digest": map[string]any{"sha256": fmt.Sprintf("%x", sha256.Sum256(data))}})
		parts := strings.Split(name, "_")
		arch := "x86_64"
		if strings.HasPrefix(parts[3], "arm64") {
			arch = "aarch64"
		}
		artifacts = append(artifacts, map[string]any{
			"name": name, "os": parts[2], "arch": arch, "size": len(data),
			"url": base + "/releases/download/" + signedTag + "/" + name, "format": "tar.gz", "bin": []string{"kae"},
			"provenance": []string{fmt.Sprintf("https://api.github.com/repos/%s/attestations/sha256:%x", repository, sha256.Sum256(data))},
		})
		if parts[2] == "linux" {
			artifacts[len(artifacts)-1].(map[string]any)["libc"] = "gnu"
		}
	}
	resources := []any{}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		resources = append(resources, map[string]any{"kind": "completion", "shell": shell, "archive": "completions/kae." + shell})
	}
	return map[string]any{
		"_type": "https://in-toto.io/Statement/v1", "predicateType": "https://packslip.dev/release/v1", "subject": subjects,
		"predicate": map[string]any{
			"project": "github.com/" + repository, "version": "0.21.0",
			"source": map[string]any{"repo": base, "tag": signedTag, "commit": signedCommit}, "artifacts": artifacts, "resources": resources,
		},
	}
}

func TestSignedReleaseMetadataControls(t *testing.T) {
	for _, defect := range []string{"", "project", "version", "source", "digest", "platform", "libc", "binary", "url", "asset", "duplicate", "resource_exec", "resource_missing", "schema"} {
		t.Run(defect, func(t *testing.T) {
			dir := t.TempDir()
			statement := signedStatement(t, dir)
			p := statement["predicate"].(map[string]any)
			artifact := p["artifacts"].([]any)[0].(map[string]any)
			switch defect {
			case "project", "version":
				p[defect] = "wrong"
			case "source":
				p["source"].(map[string]any)["commit"] = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			case "digest":
				statement["subject"].([]any)[0].(map[string]any)["digest"] = map[string]string{"sha256": strings.Repeat("0", 64)}
			case "platform":
				artifact["os"] = "windows"
			case "libc":
				// Packslip 1.4.0 and later omit libc for a static Linux build.
				for _, a := range p["artifacts"].([]any) {
					delete(a.(map[string]any), "libc")
				}
			case "binary":
				artifact["bin"] = []string{"../kae"}
			case "url":
				artifact["url"] = "https://example.com/untrusted.tar.gz"
			case "asset":
				artifact["name"] = "missing.tar.gz"
			case "duplicate":
				p["artifacts"].([]any)[1] = artifact
			case "resource_exec":
				p["resources"].([]any)[0].(map[string]any)["exec"] = []string{"kae", "init"}
			case "resource_missing":
				p["resources"] = p["resources"].([]any)[:2]
			case "schema":
				statement["predicateType"] = "https://packslip.dev/release/v999"
			}
			raw, err := json.Marshal(statement)
			if err != nil {
				t.Fatal(err)
			}
			err = validatePackslip(raw, signedTag, signedCommit, dir)
			if (err != nil) != (defect != "") {
				t.Fatalf("defect %s: %v", defect, err)
			}
		})
	}
}

func TestPackslipManifestStatesSpecLibc(t *testing.T) {
	dir := t.TempDir()
	if err := writePackslipManifest(signedTag, signedCommit, dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, packslipManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	want := ""
	for _, arch := range []string{"amd64", "arm64"} {
		want += "[[artifact]]\npath = \"" + filepath.Join(dir, "kae_0.21.0_linux_"+arch+".tar.gz") + "\"\nlibc = \"gnu\"\n\n"
	}
	if string(got) != want {
		t.Fatalf("manifest:\n%s\nwant:\n%s", got, want)
	}
}

func TestPackslipVerificationPinsSignerBeforeInspection(t *testing.T) {
	dir := t.TempDir()
	names, _ := archivesFor(signedTag)
	want := []string{"verify", "bundle", "--identity", packslipIdentity(signedTag), "--issuer", "https://token.actions.githubusercontent.com"}
	for _, name := range names {
		want = append(want, "--artifact", filepath.Join(dir, name))
	}
	err := verifyPackslip("bundle", signedTag, signedCommit, dir, dir, func(name string, args, env []string, cwd string) (string, error) {
		if name != "packslip" || !reflect.DeepEqual(args, want) || env != nil || cwd != dir {
			t.Fatalf("signature pin missing or inspection before verification: %s %v", name, args)
		}
		return "", errors.New("wrong signer")
	})
	if err == nil {
		t.Fatal("bad signature accepted")
	}
}

func TestPackslipPublicationRetry(t *testing.T) {
	for _, scenario := range []string{"fresh", "matching", "conflict", "lost_upload_reply", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			statement := signedStatement(t, dir)
			raw, err := json.Marshal(statement)
			if err != nil {
				t.Fatal(err)
			}
			exists := scenario == "matching" || scenario == "conflict"
			uploads := 0
			sourceChecks := 0
			run := func(name string, args, env []string, cwd string) (string, error) {
				if env != nil || cwd != dir {
					t.Fatal("verification context changed")
				}
				if name == "packslip" {
					if args[0] == "show" {
						if scenario == "conflict" && args[2] != "candidate" {
							return strings.Replace(string(raw), `"version":"0.21.0"`, `"version":"0.20.3"`, 1), nil
						}
						return string(raw), nil
					}
					if args[0] == "verify" {
						return "", nil
					}
				}
				if name != "gh" || len(args) < 2 {
					t.Fatalf("unexpected process: %s %v", name, args)
				}
				if args[0] == "attestation" {
					names, _ := archivesFor(signedTag)
					want := []string{
						"attestation", "verify", filepath.Join(dir, names[sourceChecks]), "--repo", repository,
						"--source-digest", signedCommit, "--source-ref", "refs/tags/" + signedTag, "--signer-workflow", repository + "/.github/workflows/release.yml",
					}
					if !reflect.DeepEqual(args, want) {
						t.Fatalf("source pin changed: %v", args)
					}
					sourceChecks++
					return "", nil
				}
				switch args[1] {
				case "view":
					if exists {
						return `{"assets":[{"name":"packslip.sigstore.json"}]}`, nil
					}
					return `{"assets":[]}`, nil
				case "download":
					if !exists || args[len(args)-1] != packslipAsset {
						t.Fatal("wrong existing asset selected")
					}
					return "", nil
				case "upload":
					want := []string{"release", "upload", signedTag, "candidate", "--repo", repository}
					if !reflect.DeepEqual(args, want) || sourceChecks != 4 {
						t.Fatalf("unsafe upload: %v", args)
					}
					uploads++
					exists = scenario != "unavailable"
					if scenario == "lost_upload_reply" || scenario == "unavailable" {
						return "", errors.New("upload response lost")
					}
					return "", nil
				}
				t.Fatalf("unexpected gh invocation: %v", args)
				return "", nil
			}
			err = publishPackslip("candidate", signedTag, signedCommit, dir, dir, run)
			if (err != nil) != (scenario == "conflict" || scenario == "unavailable") {
				t.Fatalf("scenario %s: %v", scenario, err)
			}
			if (scenario == "matching" || scenario == "conflict") && uploads != 0 {
				t.Fatal("existing asset overwritten")
			}
			if uploads > 3 {
				t.Fatal("unbounded retry")
			}
		})
	}
}

func TestVerifierToolRange(t *testing.T) {
	for _, c := range []struct {
		packslip, mise string
		ok             bool
	}{
		{"packslip 1.1.1", "2026.9.3 macos-arm64", true},
		{"packslip 1.6.0\n", "2026.10.2 macos-arm64 (2026-10-04)\n", true},
		{"packslip 1.10.0", "2027.1.0 linux-x64", true},
		{"packslip 1.1.0", "2026.10.2", false},
		{"packslip 0.9.9", "2026.10.2", false},
		{"packslip 2.0.0", "2026.10.2", false},
		{"packslip 1.6", "2026.10.2", false},
		{"packslip 1.6.0-rc1", "2026.10.2", false},
		{"other 1.6.0", "2026.10.2", false},
		{"packslip 1.6.0", "2026.9.2 macos-arm64", false},
		{"packslip 1.6.0", "mise 2026.10.2", false},
	} {
		got, err := verifierTools("repo", func(name string, args, env []string, cwd string) (string, error) {
			if len(args) != 1 || args[0] != "--version" || env != nil || cwd != "repo" {
				t.Fatalf("unexpected version probe: %s %v", name, args)
			}
			if name == "mise" {
				return c.mise, nil
			}
			return c.packslip, nil
		})
		if (err == nil) != c.ok {
			t.Fatalf("%q %q: err %v", c.packslip, c.mise, err)
		}
		if c.ok && ("packslip "+got.Packslip != strings.TrimSpace(c.packslip) || got.Mise != strings.Fields(c.mise)[0]) {
			t.Fatalf("%q %q: recorded %+v", c.packslip, c.mise, got)
		}
	}
}

func TestUnsupportedVerifierStopsBeforeDownload(t *testing.T) {
	for _, mise := range []string{"2026.10.2", "2026.9.2"} {
		_, err := verify(signedTag, t.TempDir(), t.TempDir(), "darwin", "arm64", func(name string, args, env []string, cwd string) (string, error) {
			switch name {
			case "packslip":
				if mise == "2026.10.2" {
					return "packslip 2.0.0", nil
				}
				return "packslip 1.6.0", nil
			case "mise":
				return mise, nil
			}
			t.Fatalf("ran %s before the verifier versions were accepted", name)
			return "", nil
		})
		if err == nil {
			t.Fatal("unsupported verifier accepted")
		}
	}
}

func foreignRefusal(foreign string) exitError {
	return exitError{name: "packslip", code: 1, stderr: "verification failed: bundle does not verify: Verification error: identity mismatch: expected " + foreign + ", got " + packslipIdentity(signedTag)}
}

func TestForeignSignerControl(t *testing.T) {
	dir := t.TempDir()
	names, _ := archivesFor(signedTag)
	release := packslipIdentity(signedTag)
	foreign := release[:len(release)-1]
	want := []string{"verify", "bundle", "--identity", foreign, "--issuer", "https://token.actions.githubusercontent.com"}
	for _, name := range names {
		want = append(want, "--artifact", filepath.Join(dir, name))
	}
	for outcome, ok := range map[string]bool{"refused": true, "accepted": false, "usage": false, "launch": false, "other-expected": false} {
		err := refuseForeignSigner("bundle", signedTag, signedCommit, dir, dir, func(name string, args, env []string, cwd string) (string, error) {
			if name != "packslip" || !reflect.DeepEqual(args, want) || env != nil || cwd != dir {
				t.Fatalf("control changed more than the identity: %s %v", name, args)
			}
			switch outcome {
			case "refused":
				return "", foreignRefusal(foreign)
			case "other-expected":
				// The release identity extends the foreign one, so an unanchored
				// needle would accept a mismatch that names a different expectation.
				return "", foreignRefusal(release + "-other")
			case "usage":
				return "", exitError{name: "packslip", code: 2, stderr: "error: unexpected argument"}
			case "launch":
				return "", errors.New("packslip failed: executable file not found")
			}
			return "", nil
		})
		if (err == nil) != ok {
			t.Fatalf("%s: err %v", outcome, err)
		}
		if outcome == "usage" && (err == nil || !strings.Contains(err.Error(), "unexpected argument")) {
			t.Fatalf("refusal diagnostic dropped stderr: %v", err)
		}
	}
}

// TestSignedVerificationOrder drives verify() through a receipt-era tag: the
// foreign-signer control runs after the accepted signature and statement, and a
// control that is accepted or refused for another reason stops the run before
// the native binary executes.
func TestSignedVerificationOrder(t *testing.T) {
	t.Setenv("SMOKE_WHOLE_FILE", "1")
	t.Setenv("TMPDIR", t.TempDir())
	release := packslipIdentity(signedTag)
	foreign := release[:len(release)-1]
	for _, control := range []string{"refused", "accepted", "other-reason"} {
		t.Run(control, func(t *testing.T) {
			dir := t.TempDir()
			raw, err := json.Marshal(signedStatement(t, dir))
			if err != nil {
				t.Fatal(err)
			}
			var steps []string
			run := func(name string, args, env []string, cwd string) (string, error) {
				switch {
				case name == "packslip" && args[0] == "--version":
					steps = append(steps, "packslip-version")
					return "packslip 1.6.0", nil
				case name == "mise":
					steps = append(steps, "mise-version")
					return "2026.10.2 macos-arm64", nil
				case name == "gh" && args[0] == "release":
					steps = append(steps, "download")
					return "", nil
				case name == "git":
					return signedCommit + "\n", nil
				case name == "gh" && args[0] == "attestation":
					steps = append(steps, "source")
					return "", nil
				case name == "packslip" && args[0] == "verify" && args[3] == release:
					steps = append(steps, "verify")
					return "", nil
				case name == "packslip" && args[0] == "show":
					steps = append(steps, "show")
					return string(raw), nil
				case name == "packslip" && args[0] == "verify" && args[3] == foreign:
					steps = append(steps, "control")
					switch control {
					case "refused":
						return "", foreignRefusal(foreign)
					case "other-reason":
						return "", exitError{name: "packslip", code: 1, stderr: "verification failed: network unreachable"}
					}
					return "", nil
				case name == "/bin/sh":
					steps = append(steps, "native")
					return "kae " + signedTag, nil
				case name == "bash":
					steps = append(steps, "smoke")
					return "", nil
				}
				t.Fatalf("unexpected command %s %v", name, args)
				return "", nil
			}
			got, err := verify(signedTag, dir, dir, "darwin", "arm64", run)
			if (err == nil) != (control == "refused") {
				t.Fatalf("control %s: got %+v err %v", control, got, err)
			}
			wantSteps := []string{"packslip-version", "mise-version", "download", "source", "source", "source", "source", "download", "verify", "show", "control"}
			if control == "refused" {
				wantSteps = append(wantSteps, "native", "smoke", "smoke")
				if got.Toolchain == nil || got.Toolchain.Packslip != "1.6.0" || got.Toolchain.Mise != "2026.10.2" {
					t.Fatalf("toolchain not recorded: %+v", got.Toolchain)
				}
			}
			if !reflect.DeepEqual(steps, wantSteps) {
				t.Fatalf("steps = %q; want %q", steps, wantSteps)
			}
		})
	}
}
