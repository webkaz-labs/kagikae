// Package account persists captured-account metadata (account.toml files).
// Metadata never contains secret values; payloads live in the secret backend
// under each artifact's SecretRef.
package account

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/patch"
)

// Artifact records where one captured artifact came from and where its
// payload is stored. Present=false means the artifact did not exist live at
// capture time (applying the account removes it).
type Artifact struct {
	Kind    string `toml:"kind"`
	Target  string `toml:"target"`
	Pointer string `toml:"pointer,omitempty"`
	// KeychainAccount is the captured account attribute of a KeychainReplace
	// item (codex keyring's per-login `cli|<opaque>` id), recorded verbatim so
	// apply recreates the right item. Empty for stable-account keychain items
	// (claude/cursor) and non-keychain artifacts.
	KeychainAccount string `toml:"keychain_account,omitempty"`
	SecretRef       string `toml:"secret_ref"`
	Present         bool   `toml:"present"`
}

// Account is one captured account snapshot's metadata.
type Account struct {
	Version int    `toml:"version"`
	Tool    string `toml:"tool"`
	Name    string `toml:"account"`
	Driver  string `toml:"driver"`
	// Identity is the raw login identity detected at capture (an email or
	// account id), separate from the sanitized account Name. It disambiguates
	// accounts whose identities sanitize to the same name. PII but not a secret
	// (plaintext metadata, like Name; never a token). Empty for pre-v0.8.3
	// snapshots and for a tool with no readable identity (agy) or a detection
	// failure — best-effort, never required (docs/RELEASE.md §D). Every tool now
	// exposes an identity (agy via google_accounts.json since v0.8.7), so a blank
	// value means a pre-identity snapshot or a detection failure, not "no source".
	Identity   string              `toml:"identity,omitempty"`
	CapturedAt time.Time           `toml:"captured_at"`
	Artifacts  map[string]Artifact `toml:"artifacts"`
}

// SecretRef builds the secret-backend key for one account artifact.
func SecretRef(tool, account, artifactName string) string {
	return tool + "/" + account + "/" + artifactName
}

func metaFile(dir string) string { return filepath.Join(dir, "account.toml") }

// Save writes account.toml under dir (created 0700).
func Save(dir string, acc Account) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create account dir: %w", err)
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(acc); err != nil {
		return fmt.Errorf("encode account metadata: %w", err)
	}
	return patch.WriteFileAtomic(metaFile(dir), buf.Bytes(), 0o600)
}

// Load reads one account snapshot's metadata; found=false when not captured.
func Load(dir string) (Account, bool, error) {
	var acc Account
	data, err := os.ReadFile(metaFile(dir))
	if os.IsNotExist(err) {
		return acc, false, nil
	}
	if err != nil {
		return acc, false, err
	}
	if _, err := toml.Decode(string(data), &acc); err != nil {
		return acc, false, fmt.Errorf("parse %s: %w", metaFile(dir), err)
	}
	return acc, true, nil
}

// List returns all captured accounts under accountsRoot, ordered by
// canonical tool order then account name.
func List(accountsRoot string) ([]Account, error) {
	accounts := []Account{}
	for _, tool := range constants.Tools {
		toolAccounts, err := ListForTool(accountsRoot, tool)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, toolAccounts...)
	}
	return accounts, nil
}

// ListForTool returns the captured accounts for one tool, ordered by account
// name. It reads only that tool's directory — the scoped path callers (e.g.
// completion) take to avoid walking every tool's snapshots.
func ListForTool(accountsRoot, tool string) ([]Account, error) {
	toolDir := filepath.Join(accountsRoot, tool)
	entries, err := os.ReadDir(toolDir)
	if os.IsNotExist(err) {
		return nil, nil
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
	accounts := make([]Account, 0, len(names))
	for _, name := range names {
		acc, found, err := Load(filepath.Join(toolDir, name))
		if err != nil {
			return nil, err
		}
		if found {
			accounts = append(accounts, acc)
		}
	}
	return accounts, nil
}

// ArtifactNames returns the artifact names sorted for deterministic output.
func (a Account) ArtifactNames() []string {
	names := make([]string, 0, len(a.Artifacts))
	for name := range a.Artifacts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
