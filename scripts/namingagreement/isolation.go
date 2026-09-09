package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/webkaz-labs/kagikae/tools/devtools/commandrun"
	"github.com/webkaz-labs/kagikae/tools/devtools/distribution"
)

type execute func(context.Context, commandrun.Command) (commandrun.Result, error)

func runCommand(ctx context.Context, c commandrun.Command) (commandrun.Result, error) {
	return c.Run(ctx)
}

func entries(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

func clone(env map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range env {
		out[k] = v
	}
	return out
}

func withIsolation(ctx context.Context, repo string, run execute, use func(map[string]string) error) (err error) {
	owned, err := os.MkdirTemp("", "kae-naming-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, distribution.RemoveWorkdir(owned)) }()
	result, err := run(ctx, commandrun.Command{Name: "/bin/sh", Args: []string{"-eu", "-c", `. "$1"; env -0`, "sh", filepath.Join(repo, "scripts/smoke-env.sh")}, Env: []string{"PATH=/usr/bin:/bin", "TMPDIR=" + owned}, Dir: owned, Timeout: 45 * time.Second})
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return errors.New("isolation preamble failed")
	}
	env := map[string]string{}
	for _, item := range strings.Split(result.Stdout, "\x00") {
		if item == "" {
			continue
		}
		k, v, ok := strings.Cut(item, "=")
		if !ok || k == "" {
			return errors.New("invalid preamble environment")
		}
		if _, duplicate := env[k]; duplicate {
			return errors.New("duplicate preamble environment")
		}
		env[k] = v
	}
	home := env["HOME"]
	if !filepath.IsAbs(home) {
		return errors.New("isolation preamble did not create an owned HOME")
	}
	realHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return err
	}
	realOwned, err := filepath.EvalSymlinks(owned)
	if err != nil {
		return err
	}
	info, err := os.Stat(realHome)
	if err != nil || !info.IsDir() || filepath.Dir(realHome) != realOwned {
		return errors.New("isolation preamble did not create an owned HOME")
	}
	env["TMPDIR"] = filepath.Join(realHome, "tmp")
	if err = os.Mkdir(env["TMPDIR"], 0o700); err != nil {
		return err
	}
	return use(env)
}

// resolveRoot also handles not-yet-created XDG subdirectories without ignoring
// symlinks in their existing ancestors.
func resolveRoot(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("root must be absolute")
	}
	path = filepath.Clean(path)
	base := path
	tail := []string{}
	for {
		resolved, err := filepath.EvalSymlinks(base)
		if err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if info, e := os.Lstat(base); e == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", err
		}
		parent := filepath.Dir(base)
		if parent == base {
			return "", err
		}
		tail = append(tail, filepath.Base(base))
		base = parent
	}
}

func contained(home, path string) bool {
	p, err := resolveRoot(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(home, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func preflight(ctx context.Context, env map[string]string, program string, run execute) error {
	home, err := resolveRoot(env["HOME"])
	if err != nil {
		return errors.New("isolation HOME unavailable")
	}
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR", "TMPDIR", "NAMING_LOG"} {
		if !contained(home, env[key]) {
			return fmt.Errorf("isolation root escaped: %s", key)
		}
	}
	resolved := ""
	for _, dir := range filepath.SplitList(env["PATH"]) {
		if !filepath.IsAbs(dir) {
			return errors.New("relative shim PATH")
		}
		candidate := filepath.Join(dir, "security")
		info, e := os.Stat(candidate)
		if e == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			resolved = candidate
			break
		}
	}
	if resolved != program || !contained(home, program) {
		return errors.New("security shim unavailable")
	}
	if err = os.WriteFile(env["NAMING_LOG"], nil, 0o600); err != nil {
		return err
	}
	// An absolute executable is necessary: exec's name search uses the parent's PATH.
	result, err := run(ctx, commandrun.Command{Name: program, Args: []string{"naming-preflight"}, Env: entries(env), Dir: home, Timeout: 45 * time.Second})
	if err != nil {
		return err
	}
	log, err := readLog(env["NAMING_LOG"])
	if err != nil {
		return err
	}
	if result.ExitCode != 44 || !reflect.DeepEqual(log, [][]string{{"naming-preflight"}}) {
		return errors.New("security shim did not intercept preflight")
	}
	return os.WriteFile(env["NAMING_LOG"], nil, 0o600)
}

func readLog(path string) ([][]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rows := [][]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var row []string
		if err = json.Unmarshal([]byte(line), &row); err != nil {
			return nil, errors.New("invalid security observation")
		}
		rows = append(rows, row)
	}
	return rows, nil
}
