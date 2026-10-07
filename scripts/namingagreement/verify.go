// Login-free comparison for an explicitly reviewed Claude artifact. A PATH shim
// is not an OS network sandbox or proof about direct keychain APIs or CLI binding.
package main

import (
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
	"time"

	"github.com/webkaz-labs/kagikae/tools/devtools/commandrun"
)

// Claude 2.1.288, measured 2026-10-07. A temp-HOME security shim logged
// find-generic-password for both service families, including the config-dir
// hash, a trailing slash, an empty secure-storage dir, a separate secure-storage
// dir, a decomposed non-ASCII dir, a relative value from two working
// directories, and the invalid-USER fallback. Before a digest update,
// re-establish that reachability under upstream-auth-drift's
// references/measuring.md. A version string cannot authorize new bytes.
const reviewedSHA256 = "bbe93063f7a0879a1021b2891e5c9354e5b3b98433e32efe6750f7710afed750"

func copyFile(source, target string) error {
	f, err := os.Open(source)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("source not a regular file")
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, f)
	return errors.Join(err, out.Close())
}

func verifiedCopy(source, target string) error { return copyWithDigest(source, target, reviewedSHA256) }

func copyWithDigest(source, target, digest string) error {
	if source == "" {
		return errors.New("claude unavailable; no upstream invocation")
	}
	if err := copyFile(source, target); err != nil {
		return err
	}
	f, err := os.Open(target)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, f)
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != digest {
		return errors.New("unreviewed Claude bytes; establish PATH reachability before updating the digest")
	}
	return os.Chmod(target, 0o700)
}

func installShim(ctx context.Context, root, self string, env map[string]string, run execute) error {
	shim := filepath.Join(root, "shim")
	if err := os.Mkdir(shim, 0o700); err != nil {
		return err
	}
	program := filepath.Join(shim, "security")
	if err := copyFile(self, program); err != nil {
		return err
	}
	if err := os.Chmod(program, 0o700); err != nil {
		return err
	}
	env["PATH"] = shim + ":/usr/bin:/bin"
	env["NAMING_LOG"] = filepath.Join(root, "security.jsonl")
	return preflight(ctx, env, program, run)
}

// securityMode only records argv and refuses. No operation or argument dispatch
// can reach the real security binary or another mode of this executable.
func securityMode(args []string) int {
	f, err := os.OpenFile(os.Getenv("NAMING_LOG"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return 45
	}
	err = json.NewEncoder(f).Encode(args)
	err = errors.Join(err, f.Close())
	if err != nil {
		return 45
	}
	return 44
}

func identity(argv []string) (string, string, error) {
	values := map[string]string{}
	for i, arg := range argv {
		if arg != "-s" && arg != "-a" {
			continue
		}
		if i+1 >= len(argv) || argv[i+1] == "" {
			return "", "", errors.New("unavailable service/account observation")
		}
		if _, exists := values[arg]; exists {
			return "", "", errors.New("duplicate service/account observation")
		}
		values[arg] = argv[i+1]
	}
	if values["-s"] == "" || values["-a"] == "" {
		return "", "", errors.New("unavailable service/account observation")
	}
	return values["-s"], values["-a"], nil
}

func compare(write []string, reads [][]string) error {
	if len(write) == 0 || write[0] != "add-generic-password" {
		return errors.New("unavailable production write")
	}
	service, account, err := identity(write)
	if err != nil {
		return err
	}
	if len(reads) == 0 {
		return errors.New("unavailable upstream read; empty log is not containment proof")
	}
	matched := false
	for _, row := range reads {
		if len(row) == 0 || row[0] != "find-generic-password" {
			return errors.New("unexpected upstream security operation")
		}
		s, a, err := identity(row)
		if err != nil {
			return err
		}
		matched = matched || (s == service && a == account)
	}
	if !matched {
		return errors.New("credential service/account mismatch")
	}
	return nil
}

func verifyCase(ctx context.Context, root, cwd, self, upstream string, env map[string]string, run execute) error {
	if err := preflight(ctx, env, filepath.Join(root, "shim/security"), run); err != nil {
		return err
	}
	observed, err := run(ctx, commandrun.Command{Name: self, Args: []string{"observe"}, Env: entries(env), Dir: cwd, Timeout: 45 * time.Second})
	if err != nil {
		return err
	}
	if observed.ExitCode != 0 {
		return errors.New("production observer failed")
	}
	var write []string
	if err = json.Unmarshal([]byte(observed.Stdout), &write); err != nil {
		return errors.New("invalid production observation")
	}
	if len(write) == 0 || write[0] != "add-generic-password" {
		return errors.New("unavailable production write")
	}
	if _, _, err := identity(write); err != nil {
		return err
	}
	result, err := run(ctx, commandrun.Command{Name: upstream, Args: []string{"-p", "hi"}, Env: entries(env), Dir: cwd, Timeout: 45 * time.Second})
	if err != nil {
		return err
	}
	if result.ExitCode == 0 {
		return errors.New("unexpected authenticated upstream success")
	}
	reads, err := readLog(env["NAMING_LOG"])
	if err != nil {
		return err
	}
	if err = compare(write, reads); err != nil {
		return err
	}
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() == ".credentials.json" {
			return errors.New("unexpected plaintext credential fallback")
		}
		return nil
	})
}

func verify(ctx context.Context, repo, self string, run execute, out io.Writer) error {
	upstream, err := exec.LookPath("claude")
	if err != nil {
		return errors.New("claude unavailable; no upstream invocation")
	}
	return withIsolation(ctx, repo, run, func(env map[string]string) error {
		root := env["HOME"]
		env["USER"] = "main"
		env["DISABLE_AUTOUPDATER"] = "1"
		env["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
		copied := filepath.Join(root, "claude")
		if err := verifiedCopy(upstream, copied); err != nil {
			return err
		}
		if err := installShim(ctx, root, self, env, run); err != nil {
			return err
		}
		config := filepath.Join(root, "config")
		cases := []struct {
			name  string
			extra map[string]string
		}{{"default", nil}, {"config", map[string]string{"CLAUDE_CONFIG_DIR": config}}, {"trailing", map[string]string{"CLAUDE_CONFIG_DIR": config + "/"}}, {"unicode", map[string]string{"CLAUDE_CONFIG_DIR": filepath.Join(root, "cafe\u0301")}}, {"secure", map[string]string{"CLAUDE_CONFIG_DIR": config, "CLAUDE_SECURESTORAGE_CONFIG_DIR": filepath.Join(root, "credential")}}, {"relative", map[string]string{"CLAUDE_CONFIG_DIR": "relative"}}, {"invalid_user", map[string]string{"CLAUDE_CONFIG_DIR": config, "USER": "invalid user"}}}
		for _, c := range cases {
			next := clone(env)
			for k, v := range c.extra {
				next[k] = v
			}
			cwd := filepath.Join(root, c.name)
			if err := os.Mkdir(cwd, 0o700); err != nil {
				return err
			}
			if err := verifyCase(ctx, root, cwd, self, copied, next, run); err != nil {
				return fmt.Errorf("%s: %w", c.name, err)
			}
			if err := json.NewEncoder(out).Encode(map[string]string{"case": c.name, "status": "matched"}); err != nil {
				return err
			}
		}
		return nil
	})
}
