// Package backup manages pre-write backups: metadata JSON files under the
// state dir plus payloads in the secret backend under backup/<id>/ keys.
package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/webkaz-labs/kagikae/internal/patch"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

// ArtifactRecord describes one backed-up artifact. The payload itself lives
// in the secret backend under SecretRef; Present records whether the
// artifact existed live (rollback removes it again when false).
type ArtifactRecord struct {
	Tool      string `json:"tool"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Target    string `json:"target"`
	Pointer   string `json:"pointer,omitempty"`
	SecretRef string `json:"secret_ref"`
	Present   bool   `json:"present"`
}

// Meta is the persisted backup metadata. It never contains secret values.
type Meta struct {
	SchemaVersion int               `json:"schema_version"`
	ID            string            `json:"id"`
	CreatedAt     time.Time         `json:"created_at"`
	Reason        string            `json:"reason"`
	Tools         []string          `json:"tools"`
	ActiveBefore  map[string]string `json:"active_before"`
	Artifacts     []ArtifactRecord  `json:"artifacts"`
}

// SecretRef builds the secret-backend key for one backed-up artifact.
func SecretRef(id, tool, name string) string {
	return "backup/" + id + "/" + tool + "/" + name
}

// NewID returns a unique backup id under dir, e.g. 20260611T012345Z, with a
// -2/-3 suffix on collision.
func NewID(dir string, now time.Time) string {
	base := now.UTC().Format("20060102T150405Z")
	id := base
	for n := 2; ; n++ {
		if _, err := os.Stat(metaPath(dir, id)); os.IsNotExist(err) {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, n)
	}
}

func metaPath(dir, id string) string { return filepath.Join(dir, id+".json") }

// Save writes backup metadata atomically.
func Save(dir string, meta Meta) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create backups dir: %w", err)
	}
	data, err := patch.EncodeJSON(meta)
	if err != nil {
		return err
	}
	return patch.WriteFileAtomic(metaPath(dir, meta.ID), data, 0o600)
}

// Get loads one backup's metadata.
func Get(dir, id string) (Meta, error) {
	var meta Meta
	data, err := os.ReadFile(metaPath(dir, id))
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, fmt.Errorf("parse backup %s: %w", id, err)
	}
	return meta, nil
}

// List returns all backups, newest first (by id, which is timestamp-based).
func List(dir string) ([]Meta, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []Meta{}, nil
	}
	if err != nil {
		return nil, err
	}
	metas := []Meta{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		meta, err := Get(dir, strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		metas = append(metas, meta)
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].ID > metas[j].ID })
	return metas, nil
}

// Latest returns the newest backup, or found=false when none exist.
func Latest(dir string) (Meta, bool, error) {
	metas, err := List(dir)
	if err != nil || len(metas) == 0 {
		return Meta{}, false, err
	}
	return metas[0], true, nil
}

// Delete removes one backup: payloads first, then metadata.
func Delete(ctx context.Context, be secret.Backend, dir string, meta Meta) error {
	for _, rec := range meta.Artifacts {
		if rec.Present {
			if err := be.Delete(ctx, rec.SecretRef); err != nil {
				return fmt.Errorf("delete backup payload %s: %w", rec.SecretRef, err)
			}
		}
	}
	if err := os.Remove(metaPath(dir, meta.ID)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Prune deletes the oldest backups beyond keep and returns the removed ids.
func Prune(ctx context.Context, be secret.Backend, dir string, keep int) ([]string, error) {
	metas, err := List(dir)
	if err != nil || len(metas) <= keep {
		return nil, err
	}
	removed := []string{}
	for _, meta := range metas[keep:] {
		if err := Delete(ctx, be, dir, meta); err != nil {
			return removed, err
		}
		removed = append(removed, meta.ID)
	}
	return removed, nil
}
