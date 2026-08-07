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
	Tool    string `json:"tool"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Target  string `json:"target"`
	Pointer string `json:"pointer,omitempty"`
	// KeychainAccount carries the keychain spec's account so a rollback that
	// recreates a deleted item restores it under the tool's own account
	// (e.g. cursor-user) instead of the generic fallback. Empty for non-
	// keychain artifacts and for backups written before this field existed.
	KeychainAccount string `json:"keychain_account,omitempty"`
	// JSONC marks a json-pointer Target as a JSONC document (comments and
	// trailing commas, e.g. GitHub Copilot's config.json) so a restore patches
	// it through the comment-preserving path instead of the plain-JSON one,
	// which rejects the leading // comments. Empty for plain-JSON artifacts and
	// for backups written before this field existed.
	JSONC bool `json:"jsonc,omitempty"`
	// KeychainReplace is **legacy**: kae no longer writes it. It marked codex's
	// keyring item back when its account was modelled as an opaque per-login id and
	// its service as holding one item, and it meant "delete every item of the
	// service, then write" — which deleted another CODEX_HOME's login. Records
	// carrying it restore as KeychainMatchAccount (specFromRecord): the recorded
	// account is the item's, and only that item may be touched.
	KeychainReplace bool `json:"keychain_replace,omitempty"`
	// KeychainMatchAccount marks a keychain item identified by service **and**
	// account, because the service can hold more than one legitimate item: a
	// service shared with other tools (agy's gemini/antigravity) or one item per
	// tool home (codex's `Codex Auth`). A rollback then reads/writes/deletes only
	// that account's item and never a sibling. Empty for single-item services
	// (claude, cursor) and older backups.
	KeychainMatchAccount bool `json:"keychain_match_account,omitempty"`
	// Every field above is a fact about the captured artifact, needed to write it
	// back. Nothing here records *policy* — whether losing a payload is
	// survivable, for instance — because policy belongs to the code that restores:
	// an old backup must not pin a decision kae has since changed.
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
	return secret.NSBackup + "/" + id + "/" + tool + "/" + name
}

// NewID returns a unique backup id under dir, e.g. 20260611T012345Z, with a
// -02/-03 suffix on collision.
//
// The suffix is **zero-padded** because every consumer orders backups by comparing the
// id as a string (List sorts descending), and an unpadded one sorts `-2` above `-10`:
// the tenth backup in a single second would then read as older than the second. Reaching
// that needs ten backups inside one clock second, which `kae run -s` halved the distance
// to when a declined recapture made it emit two per command. Old ids cannot share a
// second with new ones, so widening the field re-sorts nothing that exists.
//
// Two digits is a **bound, not a cure**: the hundredth backup in one second sorts below
// the ninety-ninth. Each create does a collision `Stat` walk plus two atomic writes, so
// that is not a count any command reaches; the width is chosen to sit far past what is,
// not to make the class impossible.
func NewID(dir string, now time.Time) string {
	base := now.UTC().Format("20060102T150405Z")
	id := base
	for n := 2; ; n++ {
		if _, err := os.Stat(metaPath(dir, id)); os.IsNotExist(err) {
			return id
		}
		id = fmt.Sprintf("%s-%02d", base, n)
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
//
// keep counts only the backups `counts` accepts; anything it rejects is retained without
// consuming a slot, and is pruned once it is older than the oldest kept accepted one. A
// nil counts accepts everything.
//
// The seam exists because not every backup is the same kind of thing and this package
// deliberately does not know kae's reason vocabulary. One reason records a copy kae
// *declined to adopt* rather than a state it was about to change, and a single command can
// write one of each — so counting them alike let the preserved copy, which is created
// last and therefore sorts newest, evict the very backup it was meant to sit beside. At
// `backup_keep = 1` that left the run with no undo target at all.
func Prune(ctx context.Context, be secret.Backend, dir string, keep int, counts func(Meta) bool) ([]string, error) {
	metas, err := List(dir)
	if err != nil {
		return nil, err
	}
	removed := []string{}
	counted, cutoff := 0, ""
	for _, meta := range metas { // newest first
		if counts != nil && !counts(meta) {
			continue
		}
		counted++
		if counted == keep {
			cutoff = meta.ID
			break
		}
	}
	if counted < keep {
		// A statement of intent, not a filter: with fewer than `keep` countable backups the
		// cutoff stays "" and the loop below deletes nothing either, since every id is `>= ""`.
		// Said here so nobody adds a test that cannot fail, or reports the line as uncovered.
		//
		// It is also where the retention of *uncountable* backups is bounded, and the bound is
		// external: every declining `kae run -s` writes one countable backup beside its
		// preserved copy, so an uncountable one always has a countable sibling to age against.
		// A future path emitting a preserved copy with no countable sibling would make this
		// return retain them forever.
		return nil, nil
	}
	for _, meta := range metas {
		if meta.ID >= cutoff { // ids sort as strings, newest first (see NewID)
			continue
		}
		if err := Delete(ctx, be, dir, meta); err != nil {
			return removed, err
		}
		removed = append(removed, meta.ID)
	}
	return removed, nil
}
