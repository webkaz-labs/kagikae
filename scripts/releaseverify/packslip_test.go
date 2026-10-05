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
	for _, defect := range []string{"", "project", "version", "source", "digest", "platform", "binary", "url", "asset", "duplicate", "resource_exec", "resource_missing", "schema"} {
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
	for version, ok := range map[string]bool{
		"packslip 1.1.1": true, "packslip 1.6.0\n": true, "packslip 1.10.0": true,
		"packslip 1.1.0": false, "packslip 0.9.9": false, "packslip 2.0.0": false,
		"packslip 1.6": false, "packslip 1.6.0-rc1": false, "other 1.6.0": false,
	} {
		got, err := verifierTools("repo", func(name string, args, env []string, cwd string) (string, error) {
			if len(args) != 1 || args[0] != "--version" || env != nil || cwd != "repo" {
				t.Fatalf("unexpected version probe: %s %v", name, args)
			}
			if name == "mise" {
				return "2026.10.2 macos-arm64 (2026-10-04)\n", nil
			}
			return version, nil
		})
		if (err == nil) != ok {
			t.Fatalf("%q: err %v", version, err)
		}
		if ok && (got.Mise != "2026.10.2" || "packslip "+got.Packslip != strings.TrimSpace(version)) {
			t.Fatalf("%q: recorded %+v", version, got)
		}
	}
}

func TestUnsupportedVerifierStopsBeforeDownload(t *testing.T) {
	_, err := verify(signedTag, t.TempDir(), t.TempDir(), "darwin", "arm64", func(name string, args, env []string, cwd string) (string, error) {
		if name != "packslip" {
			t.Fatalf("ran %s before the verifier version was accepted", name)
		}
		return "packslip 2.0.0", nil
	})
	if err == nil {
		t.Fatal("unsupported verifier accepted")
	}
}

func TestForeignSignerControl(t *testing.T) {
	dir := t.TempDir()
	names, _ := archivesFor(signedTag)
	foreign := strings.TrimSuffix(packslipIdentity(signedTag), "0")
	want := []string{"verify", "bundle", "--identity", foreign, "--issuer", "https://token.actions.githubusercontent.com"}
	for _, name := range names {
		want = append(want, "--artifact", filepath.Join(dir, name))
	}
	if !strings.HasPrefix(packslipIdentity(signedTag), foreign) || foreign == packslipIdentity(signedTag) {
		t.Fatal("control identity must be a strict prefix of the release identity")
	}
	for outcome, ok := range map[string]bool{"refused": true, "accepted": false, "usage": false, "launch": false} {
		err := refuseForeignSigner("bundle", signedTag, signedCommit, dir, dir, func(name string, args, env []string, cwd string) (string, error) {
			if name != "packslip" || !reflect.DeepEqual(args, want) || env != nil || cwd != dir {
				t.Fatalf("control changed more than the identity: %s %v", name, args)
			}
			switch outcome {
			case "refused":
				return "", exitError{name: "packslip", code: 1, stderr: "verification failed: bundle does not verify: Verification error: identity mismatch: expected " + foreign + ", got " + packslipIdentity(signedTag)}
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
	}
}
