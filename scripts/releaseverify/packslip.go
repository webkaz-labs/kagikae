package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/webkaz-labs/kagikae/tools/devtools/distribution"
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
	return releaseSpec(tag, commit).VerifySource(dir, repo, distribution.Command(run))
}

func verifyPackslip(bundle, tag, commit, dir, repo string, run commandFunc) error {
	if !packslipRelease(tag) || !commitPattern.MatchString(commit) {
		return errors.New("invalid signed release identity")
	}
	return releaseSpec(tag, commit).VerifyPackslip(bundle, dir, repo, distribution.Command(run))
}

func validatePackslip(raw []byte, tag, commit, dir string) error {
	return releaseSpec(tag, commit).ValidateStatement(raw, dir)
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
