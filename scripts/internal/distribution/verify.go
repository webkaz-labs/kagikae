package distribution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/webkaz-labs/kagikae/scripts/internal/commandrun"
)

type Command func(string, []string, []string, string) (string, error)

// VerifyPackslip does not expose decoded metadata until signature verification
// succeeds. run must treat launch, cancellation and nonzero exits as failures.
func (s Spec) VerifyPackslip(bundle, dir, cwd string, run Command) error {
	return s.verifyPackslip(bundle, dir, cwd, run, "")
}

func (s Spec) verifyPackslip(bundle, dir, cwd string, run Command, fixtureKey string) error {
	if err := s.Validate(); err != nil {
		return err
	}
	args := []string{"verify", bundle, "--identity", s.Identity, "--issuer", s.Issuer}
	if fixtureKey != "" {
		args = []string{"verify", bundle, "--pubkey", fixtureKey, "--allow-unlogged"}
	}
	for _, a := range s.Artifacts {
		args = append(args, "--artifact", filepath.Join(dir, a.Name))
	}
	if _, err := run("packslip", args, nil, cwd); err != nil {
		return fmt.Errorf("packslip signature/artifact verification: %w", err)
	}
	raw, err := run("packslip", []string{"show", "--raw", bundle}, nil, cwd)
	if err != nil {
		return err
	}
	return s.ValidateStatement([]byte(raw), dir)
}

func (s Spec) VerifySource(dir, cwd string, run Command) error {
	if err := s.Validate(); err != nil {
		return err
	}
	for _, a := range s.Artifacts {
		args := []string{"attestation", "verify", filepath.Join(dir, a.Name), "--repo", s.Repository, "--source-digest", s.Commit, "--source-ref", "refs/tags/" + s.Tag, "--signer-workflow", s.Repository + "/" + s.Workflow}
		if _, err := run("gh", args, nil, cwd); err != nil {
			return err
		}
	}
	return nil
}

// Verified retains private verified bytes rather than returning a staging path
// that another process could replace between verification and execution.
type Verified struct {
	spec     Spec
	payloads map[string][]byte
	assets   map[string][]byte
}

func Verify(s Spec, dir, bundle, cwd string, run Command) (Verified, error) {
	return verify(s, dir, bundle, cwd, run, "")
}

// VerifyFixture verifies explicitly trusted synthetic releases with a pinned local
// key. It does not verify build provenance and must not be reported as published
// release verification. Production Verify has no key/unlogged override.
func VerifyFixture(s Spec, dir, bundle, cwd, key string, run Command) (Verified, error) {
	if !filepath.IsAbs(key) {
		return Verified{}, errors.New("fixture public key must be absolute")
	}
	return verify(s, dir, bundle, cwd, run, key)
}

func verify(s Spec, dir, bundle, cwd string, run Command, fixtureKey string) (got Verified, err error) {
	if err := s.Validate(); err != nil {
		return Verified{}, err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return Verified{}, err
	}
	s, err = ParseSpec(raw)
	if err != nil {
		return Verified{}, err
	}
	stage, err := os.MkdirTemp("", "distribution-verify-")
	if err != nil {
		return Verified{}, err
	}
	defer func() {
		if e := RemoveWorkdir(stage); e != nil {
			got = Verified{}
			err = errors.Join(err, e)
		}
	}()
	archives, err := filepath.Glob(filepath.Join(dir, "*.tar.gz"))
	if err != nil || len(archives) != len(s.Artifacts) {
		return Verified{}, errors.New("unexpected downloaded archive set")
	}
	names := []string{"checksums.txt"}
	for _, a := range s.Artifacts {
		names = append(names, a.Name)
	}
	assets := map[string][]byte{}
	for _, name := range names {
		data, e := readRegular(filepath.Join(dir, name))
		if e != nil {
			return Verified{}, e
		}
		assets[name] = data
		if e = os.WriteFile(filepath.Join(stage, name), data, 0o600); e != nil {
			return Verified{}, e
		}
	}
	data, e := readRegular(bundle)
	if e != nil {
		return Verified{}, e
	}
	bundle = filepath.Join(stage, "packslip.sigstore.json")
	if e = os.WriteFile(bundle, data, 0o600); e != nil {
		return Verified{}, e
	}
	dir = stage
	if err := VerifyArchives(dir, s.Artifacts); err != nil {
		return Verified{}, err
	}
	if fixtureKey == "" {
		if err := s.VerifySource(dir, cwd, run); err != nil {
			return Verified{}, err
		}
	}
	if err := s.verifyPackslip(bundle, dir, cwd, run, fixtureKey); err != nil {
		return Verified{}, err
	}
	payloads := map[string][]byte{}
	for _, a := range s.Artifacts {
		data, err := ReadArchive(filepath.Join(dir, a.Name), a.MemberSet(), a.Binary)
		if err != nil {
			return Verified{}, err
		}
		payloads[a.Name] = data
	}
	return Verified{spec: s, payloads: payloads, assets: assets}, nil
}

// RunNative owns a fresh HOME/XDG environment. This isolates configuration, not
// network access, keychains or arbitrary effects of the verified program.
func (v Verified) RunNative(ctx context.Context, artifact string, args []string, expected string) (err error) {
	data, ok := v.payloads[artifact]
	if !ok {
		return errors.New("artifact not verified")
	}
	root, err := os.MkdirTemp("", "distribution-exec-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, RemoveWorkdir(root)) }()
	binary := filepath.Join(root, "verified-binary")
	if err = os.WriteFile(binary, data, 0o700); err != nil {
		return err
	}
	env, err := IsolatedEnvironment(root)
	if err != nil {
		return err
	}
	result, err := (commandrun.Command{Name: binary, Args: args, Env: env, Dir: root, Timeout: 2 * time.Minute}).Run(ctx)
	if err != nil {
		return err
	}
	if result.ExitCode != 0 || result.Stdout != expected {
		return errors.New("native command output or exit mismatch")
	}
	return nil
}

func IsolatedEnvironment(root string) ([]string, error) {
	env := []string{"PATH=/usr/bin:/bin", "HOME=" + root, "NO_COLOR=1"}
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR", "TMPDIR"} {
		p := filepath.Join(root, key)
		if err := os.MkdirAll(p, 0o700); err != nil {
			return nil, err
		}
		env = append(env, key+"="+p)
	}
	return env, nil
}

func readRegular(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("release input not regular")
	}
	return os.ReadFile(path)
}

// RemoveWorkdir restores owner directory access without following symlinks.
func RemoveWorkdir(root string) error {
	err := filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			return os.Chmod(p, 0o700)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return os.RemoveAll(root)
}
