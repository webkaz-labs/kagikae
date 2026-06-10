// Package envprofile stores env-mode profiles: named sets of environment
// variables (API keys, long-lived tokens) per tool account. Metadata holds
// only variable names; values live in the secret backend under
// env/<tool>/<account>/<VAR>.
package envprofile

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/patch"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

var varNameRE = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)

// ValidVarName reports whether name is a safe environment variable name.
func ValidVarName(name string) bool { return varNameRE.MatchString(name) }

// Profile is one env profile's metadata (never values).
type Profile struct {
	Version   int       `toml:"version"`
	Tool      string    `toml:"tool"`
	Account   string    `toml:"account"`
	UpdatedAt time.Time `toml:"updated_at"`
	Vars      []string  `toml:"vars"`
}

// SecretRef builds the secret-backend key for one profile variable.
func SecretRef(tool, account, varName string) string {
	return "env/" + tool + "/" + account + "/" + varName
}

func metaFile(dir string) string { return filepath.Join(dir, "env.toml") }

// Save writes the profile metadata (vars sorted for determinism).
func Save(dir string, profile Profile) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create env profile dir: %w", err)
	}
	sort.Strings(profile.Vars)
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(profile); err != nil {
		return fmt.Errorf("encode env profile: %w", err)
	}
	return patch.WriteFileAtomic(metaFile(dir), buf.Bytes(), 0o600)
}

// Load reads one profile; found=false when it does not exist.
func Load(dir string) (Profile, bool, error) {
	var profile Profile
	data, err := os.ReadFile(metaFile(dir))
	if os.IsNotExist(err) {
		return profile, false, nil
	}
	if err != nil {
		return profile, false, err
	}
	if _, err := toml.Decode(string(data), &profile); err != nil {
		return profile, false, fmt.Errorf("parse %s: %w", metaFile(dir), err)
	}
	return profile, true, nil
}

// List returns all env profiles under root, ordered by canonical tool order
// then account name.
func List(root string) ([]Profile, error) {
	profiles := []Profile{}
	for _, tool := range constants.Tools {
		toolDir := filepath.Join(root, tool)
		entries, err := os.ReadDir(toolDir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		names := []string{}
		for _, entry := range entries {
			if entry.IsDir() {
				names = append(names, entry.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			profile, found, err := Load(filepath.Join(toolDir, name))
			if err != nil {
				return nil, err
			}
			if found {
				profiles = append(profiles, profile)
			}
		}
	}
	return profiles, nil
}

// Delete removes the profile metadata and all its secret payloads.
func Delete(ctx context.Context, be secret.Backend, dir string, profile Profile) error {
	for _, varName := range profile.Vars {
		if err := be.Delete(ctx, SecretRef(profile.Tool, profile.Account, varName)); err != nil {
			return fmt.Errorf("delete env value %s: %w", varName, err)
		}
	}
	if err := os.Remove(metaFile(dir)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// EnvStrings resolves the profile's variables into KEY=VALUE strings.
func EnvStrings(ctx context.Context, be secret.Backend, profile Profile) ([]string, error) {
	pairs := make([]string, 0, len(profile.Vars))
	vars := append([]string(nil), profile.Vars...)
	sort.Strings(vars)
	for _, varName := range vars {
		value, found, err := be.Get(ctx, SecretRef(profile.Tool, profile.Account, varName))
		if err != nil {
			return nil, fmt.Errorf("read env value %s: %w", varName, err)
		}
		if !found {
			return nil, fmt.Errorf("env value %s is missing from the secret store; re-run kae env set", varName)
		}
		pairs = append(pairs, varName+"="+string(value))
	}
	return pairs, nil
}
