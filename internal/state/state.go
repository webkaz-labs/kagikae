// Package state persists kagikae's belief about the active accounts.
// It records what kae last applied; live truth is re-verified by status.
package state

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/patch"
)

// State is the persisted content of state.json.
type State struct {
	SchemaVersion int               `json:"schema_version"`
	ActiveProfile string            `json:"active_profile,omitempty"`
	Active        map[string]string `json:"active"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

// New returns an empty state.
func New() *State {
	return &State{SchemaVersion: constants.SchemaVersion, Active: map[string]string{}}
}

// Load reads state.json; a missing file yields an empty state.
func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return New(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	st := New()
	if err := json.Unmarshal(data, st); err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}
	if st.Active == nil {
		st.Active = map[string]string{}
	}
	return st, nil
}

// Save writes state.json atomically (0600 under a 0700 dir).
func Save(path string, st *State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(st); err != nil {
		return err
	}
	return patch.WriteFileAtomic(path, buf.Bytes(), 0o600)
}
