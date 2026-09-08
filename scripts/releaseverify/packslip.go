package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

const packslipAsset = "packslip.sigstore.json"

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// v0.21.0 is the first signed-distribution release; older tags retain their
// archive and provenance checks without acquiring a retroactive requirement.
func packslipRelease(tag string) bool {
	if !tagPattern.MatchString(tag) {
		return false
	}
	parts := strings.Split(tag[1:], ".")
	major, err1 := strconv.ParseUint(parts[0], 10, 64)
	minor, err2 := strconv.ParseUint(parts[1], 10, 64)
	return err1 == nil && err2 == nil && (major > 0 || minor >= 21)
}

func packslipIdentity(tag string) string {
	return "https://github.com/" + repository + "/.github/workflows/release.yml@refs/tags/" + tag
}

func preparePackslip(tag, commit, dir, repo string, run commandFunc) error {
	if !packslipRelease(tag) || !commitPattern.MatchString(commit) {
		return errors.New("expected receipt-era release tag and full source commit")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		return errors.New("published archive staging directory must be empty")
	}
	if _, err := run("gh", []string{"release", "download", tag, "--repo", repository, "--dir", dir, "--pattern", "checksums.txt", "--pattern", "kae_*.tar.gz"}, nil, repo); err != nil {
		return err
	}
	names, _ := archivesFor(tag)
	if err := verifyArchives(dir, names); err != nil {
		return err
	}
	return verifySource(tag, commit, dir, names, repo, run)
}

func verifySource(tag, commit, dir string, names []string, repo string, run commandFunc) error {
	for _, name := range names {
		args := []string{
			"attestation", "verify", filepath.Join(dir, name), "--repo", repository,
			"--source-digest", commit, "--source-ref", "refs/tags/" + tag,
			"--signer-workflow", repository + "/.github/workflows/release.yml",
		}
		if _, err := run("gh", args, nil, repo); err != nil {
			return err
		}
	}
	return nil
}

func verifyPackslip(bundle, tag, commit, dir, repo string, run commandFunc) error {
	if !packslipRelease(tag) || !commitPattern.MatchString(commit) {
		return errors.New("invalid signed release identity")
	}
	names, _ := archivesFor(tag)
	args := []string{"verify", bundle, "--identity", packslipIdentity(tag), "--issuer", "https://token.actions.githubusercontent.com"}
	for _, name := range names {
		args = append(args, "--artifact", filepath.Join(dir, name))
	}
	if _, err := run("packslip", args, nil, repo); err != nil {
		return fmt.Errorf("packslip signature/artifact verification: %w", err)
	}
	raw, err := run("packslip", []string{"show", "--raw", bundle}, nil, repo)
	if err != nil {
		return err
	}
	return validatePackslip([]byte(raw), tag, commit, dir)
}

func validatePackslip(raw []byte, tag, commit, dir string) error {
	var statement struct {
		Type          string `json:"_type"`
		PredicateType string `json:"predicateType"`
		Subject       []struct {
			Name   string            `json:"name"`
			Digest map[string]string `json:"digest"`
		} `json:"subject"`
		Predicate struct {
			Project string `json:"project"`
			Version string `json:"version"`
			Source  struct {
				Repo   string `json:"repo"`
				Commit string `json:"commit"`
				Tag    string `json:"tag"`
			} `json:"source"`
			Artifacts []map[string]json.RawMessage `json:"artifacts"`
			Resources []map[string]string          `json:"resources"`
		} `json:"predicate"`
	}
	if len(raw) > 1024*1024 || json.Unmarshal(raw, &statement) != nil {
		return errors.New("invalid packslip statement")
	}
	p := statement.Predicate
	base := "https://github.com/" + repository
	if statement.Type != "https://in-toto.io/Statement/v1" || statement.PredicateType != "https://packslip.dev/release/v1" ||
		p.Project != "github.com/"+repository || p.Version != strings.TrimPrefix(tag, "v") ||
		p.Source.Repo != base || p.Source.Tag != tag || p.Source.Commit != commit {
		return errors.New("packslip project/version/source mismatch")
	}
	names, err := archivesFor(tag)
	if err != nil {
		return err
	}
	if len(statement.Subject) != len(names) || len(p.Artifacts) != len(names) {
		return errors.New("packslip archive set mismatch")
	}
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		subjects, artifacts := 0, 0
		for _, subject := range statement.Subject {
			if subject.Name == name {
				subjects++
				if subject.Digest["sha256"] != hex.EncodeToString(digest[:]) {
					return errors.New("packslip subject digest mismatch")
				}
			}
		}
		parts := strings.Split(name, "_")
		arch := "x86_64"
		if strings.HasPrefix(parts[3], "arm64") {
			arch = "aarch64"
		}
		for _, artifact := range p.Artifacts {
			var gotName string
			if json.Unmarshal(artifact["name"], &gotName) != nil || gotName != name {
				continue
			}
			artifacts++
			want := map[string]any{
				"name": name, "os": parts[2], "arch": arch, "size": float64(len(data)),
				"url": base + "/releases/download/" + tag + "/" + name, "format": "tar.gz", "bin": []any{"kae"},
				"provenance": []any{"https://api.github.com/repos/" + repository + "/attestations/sha256:" + hex.EncodeToString(digest[:])},
			}
			if parts[2] == "linux" {
				want["libc"] = "gnu"
			}
			for key, value := range want {
				var got any
				if json.Unmarshal(artifact[key], &got) != nil || !reflect.DeepEqual(got, value) {
					return fmt.Errorf("packslip artifact %s mismatch: %s", name, key)
				}
			}
			for key := range artifact {
				if _, known := want[key]; !known && key != "requires" {
					return fmt.Errorf("unexpected packslip artifact field: %s", key)
				}
			}
		}
		if subjects != 1 || artifacts != 1 {
			return errors.New("packslip duplicate or missing archive")
		}
	}
	wantedResources := map[string]string{"bash": "completions/kae.bash", "zsh": "completions/kae.zsh", "fish": "completions/kae.fish"}
	if len(p.Resources) != len(wantedResources) {
		return errors.New("packslip completion resource set mismatch")
	}
	for _, resource := range p.Resources {
		shell := resource["shell"]
		path, exists := wantedResources[shell]
		if !exists || !reflect.DeepEqual(resource, map[string]string{"kind": "completion", "shell": shell, "archive": path}) {
			return errors.New("packslip completion resource mismatch")
		}
		delete(wantedResources, shell)
	}
	return nil
}

// publishPackslip never clobbers an asset. After an uncertain upload, inspect the
// release again: a matching signature/statement is success; a conflict is fatal.
func publishPackslip(bundle, tag, commit, dir, repo string, run commandFunc) error {
	names, _ := archivesFor(tag)
	if err := verifyArchives(dir, names); err != nil {
		return err
	}
	if err := verifySource(tag, commit, dir, names, repo, run); err != nil {
		return err
	}
	if err := verifyPackslip(bundle, tag, commit, dir, repo, run); err != nil {
		return err
	}
	for attempt := 0; attempt < 3; attempt++ {
		view, err := run("gh", []string{"release", "view", tag, "--repo", repository, "--json", "assets"}, nil, repo)
		if err != nil {
			return err
		}
		var release struct {
			Assets []struct{ Name string } `json:"assets"`
		}
		if err := json.Unmarshal([]byte(view), &release); err != nil {
			return err
		}
		for _, asset := range release.Assets {
			if asset.Name != packslipAsset {
				continue
			}
			existing, err := os.MkdirTemp(dir, "published-bundle-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(existing)
			if _, err := run("gh", []string{"release", "download", tag, "--repo", repository, "--dir", existing, "--pattern", packslipAsset}, nil, repo); err != nil {
				return err
			}
			if err := verifyPackslip(filepath.Join(existing, packslipAsset), tag, commit, dir, repo, run); err != nil {
				return fmt.Errorf("existing packslip conflicts; refusing overwrite: %w", err)
			}
			return nil
		}
		if _, err := run("gh", []string{"release", "upload", tag, bundle, "--repo", repository}, nil, repo); err == nil {
			// Verify the bytes actually published on the next iteration as well.
			continue
		}
	}
	return errors.New("packslip upload not confirmed after bounded attempts; rerun only the packslip job")
}

func packslipJob(ctx context.Context, args []string) (result, int) {
	if len(args) < 4 || (args[0] != "--packslip-prepare" && args[0] != "--packslip-publish") ||
		(len(args) != 4 && args[0] == "--packslip-prepare") || (len(args) != 5 && args[0] == "--packslip-publish") {
		return result{Status: "failed", Reason: "usage: --packslip-prepare TAG COMMIT DIR | --packslip-publish TAG COMMIT DIR BUNDLE"}, 1
	}
	tag, commit, dir := args[1], args[2], args[3]
	if !packslipRelease(tag) || !commitPattern.MatchString(commit) || !filepath.IsAbs(dir) {
		return result{Status: "failed", Reason: "invalid tag, commit or absolute staging directory"}, 1
	}
	repo, err := os.Getwd()
	if err != nil {
		return result{Status: "failed", Reason: err.Error()}, 1
	}
	run := func(name string, argv, env []string, cwd string) (string, error) {
		return commandContext(ctx, name, argv, env, cwd)
	}
	if args[0] == "--packslip-prepare" {
		err = preparePackslip(tag, commit, dir, repo, run)
	} else {
		err = publishPackslip(args[4], tag, commit, dir, repo, run)
	}
	if err != nil {
		return result{Status: "failed", Tag: tag, Reason: err.Error()}, 1
	}
	return result{Status: "success", Tag: tag, Packslip: args[0]}, 0
}
