package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/webkaz-labs/kagikae/tools/devtools/distribution"
)

const packslipAsset = "packslip.sigstore.json"

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// v0.21.0 is the first signed-distribution release; older tags retain their
// archive and provenance checks without acquiring a retroactive requirement.
func packslipRelease(tag string) bool { return releaseFrom(tag, [3]uint64{0, 21, 0}) }

func packslipIdentity(tag string) string {
	return "https://github.com/" + repository + "/.github/workflows/release.yml@refs/tags/" + tag
}

// The release/v1 format is unchanged within Packslip 1.x (upstream 1.2.0-1.6.0
// notes), so the verifier accepts that major from the oldest version exercised.
var minimumPackslip = [3]uint64{1, 1, 1}

// verifierTools checks both floating verifiers before anything is downloaded and
// returns the versions this run exercises.
func verifierTools(repo string, run commandFunc) (*tools, error) {
	out, err := run("packslip", []string{"--version"}, nil, repo)
	if err != nil {
		return nil, err
	}
	version, named := strings.CutPrefix(strings.TrimSpace(out), "packslip ")
	got, ok := distribution.Triple(version)
	if !named || !ok {
		return nil, fmt.Errorf("unrecognized packslip version %q", strings.TrimSpace(out))
	}
	if got[0] != minimumPackslip[0] || slices.Compare(got[:], minimumPackslip[:]) < 0 {
		return nil, fmt.Errorf("verifier requires packslip >=1.1.1,<2; found %s", strings.TrimSpace(out))
	}
	out, err = run("mise", []string{"--version"}, nil, repo)
	if err != nil {
		return nil, err
	}
	mise, err := distribution.MiseAtLeast(out, distribution.MinimumMise)
	if err != nil {
		return nil, err
	}
	return &tools{Packslip: version, Mise: mise}, nil
}

// refuseForeignSigner repeats the accepted verification with one change, an
// identity that is a strict prefix of the release's, and requires Packslip's
// identity refusal naming that identity, not merely a nonzero exit (usage errors
// also exit nonzero). It runs only after the exact identity verified the bytes.
func refuseForeignSigner(bundle, tag, commit, dir, repo string, run commandFunc) error {
	spec := releaseSpec(tag, commit)
	foreign := spec.Identity[:len(spec.Identity)-1]
	args := []string{"verify", bundle, "--identity", foreign, "--issuer", spec.Issuer}
	for _, a := range spec.Artifacts {
		args = append(args, "--artifact", filepath.Join(dir, a.Name))
	}
	_, err := run("packslip", args, nil, repo)
	if err == nil {
		return errors.New("packslip accepted a foreign signer identity " + foreign)
	}
	var exit exitError
	if !errors.As(err, &exit) {
		return fmt.Errorf("foreign signer control did not complete: %w", err)
	}
	if !strings.Contains(exit.stderr, "identity mismatch: expected "+foreign+", got ") {
		return fmt.Errorf("packslip refused the foreign signer identity for another reason: %v: %s", err, boundedStderr(exit.stderr))
	}
	return nil
}

// boundedStderr keeps a refusal diagnostic readable without letting an
// unexpectedly long stderr into the result.
func boundedStderr(stderr string) string {
	const limit = 512
	text := strings.TrimSpace(stderr)
	if len(text) > limit {
		return text[:limit] + "..."
	}
	return text
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
	if err := verifySource(tag, commit, dir, names, repo, run); err != nil {
		return err
	}
	return writePackslipManifest(tag, commit, dir)
}

// packslipManifestFile is the signing Action's manifest input, written beside
// the verified archives; the Action's artifacts glob does not collect it.
const packslipManifestFile = "release.toml"

// writePackslipManifest states each artifact's C library from releaseSpec, so the
// signer records the libc the verifier requires. Packslip 1.4.0 and later omit
// libc for a static Linux build unless the manifest names it, which would also
// select the archive on musl hosts.
func writePackslipManifest(tag, commit, dir string) error {
	var b strings.Builder
	for _, a := range releaseSpec(tag, commit).Artifacts {
		if a.Libc == "" {
			continue
		}
		// A JSON string is also a valid TOML basic string.
		path, err := json.Marshal(filepath.Join(dir, a.Name))
		if err != nil {
			return err
		}
		libc, err := json.Marshal(a.Libc)
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "[[artifact]]\npath = %s\nlibc = %s\n\n", path, libc)
	}
	return os.WriteFile(filepath.Join(dir, packslipManifestFile), []byte(b.String()), 0o600)
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
