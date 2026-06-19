package backup

import (
	"context"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/testutil/secrettest"
)

func meta(id string, present bool) Meta {
	return Meta{
		SchemaVersion: 1,
		ID:            id,
		CreatedAt:     time.Date(2026, 6, 11, 1, 0, 0, 0, time.UTC),
		Reason:        "switch",
		Tools:         []string{"claude"},
		ActiveBefore:  map[string]string{"claude": "side"},
		Artifacts: []ArtifactRecord{{
			Tool: "claude", Name: "oauth", Kind: "file", Target: "/x",
			SecretRef: SecretRef(id, "claude", "oauth"), Present: present,
		}},
	}
}

func TestNewIDCollision(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 11, 1, 23, 45, 0, time.UTC)
	id := NewID(dir, now)
	if id != "20260611T012345Z" {
		t.Fatalf("unexpected id: %s", id)
	}
	if err := Save(dir, meta(id, true)); err != nil {
		t.Fatal(err)
	}
	id2 := NewID(dir, now)
	if id2 != "20260611T012345Z-2" {
		t.Fatalf("unexpected collision id: %s", id2)
	}
}

func TestSaveListLatestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"20260611T010000Z", "20260611T020000Z"} {
		if err := Save(dir, meta(id, true)); err != nil {
			t.Fatal(err)
		}
	}
	metas, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 2 || metas[0].ID != "20260611T020000Z" {
		t.Fatalf("expected newest first: %+v", metas)
	}
	latest, found, err := Latest(dir)
	if err != nil || !found || latest.ID != "20260611T020000Z" {
		t.Fatalf("latest: %+v %v %v", latest, found, err)
	}
	got, err := Get(dir, "20260611T010000Z")
	if err != nil || got.ActiveBefore["claude"] != "side" {
		t.Fatalf("get: %+v %v", got, err)
	}
}

func TestListEmptyDir(t *testing.T) {
	metas, err := List(t.TempDir() + "/missing")
	if err != nil || metas == nil || len(metas) != 0 {
		t.Fatalf("expected empty slice: %v %v", metas, err)
	}
}

func TestPruneDeletesPayloads(t *testing.T) {
	dir := t.TempDir()
	be := secrettest.NewMem()
	ctx := context.Background()
	ids := []string{"20260611T010000Z", "20260611T020000Z", "20260611T030000Z"}
	for _, id := range ids {
		m := meta(id, true)
		if err := be.Set(ctx, m.Artifacts[0].SecretRef, []byte("payload")); err != nil {
			t.Fatal(err)
		}
		if err := Save(dir, m); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := Prune(ctx, be, dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != ids[0] {
		t.Fatalf("unexpected removed: %v", removed)
	}
	if _, ok := be.Values[SecretRef(ids[0], "claude", "oauth")]; ok {
		t.Fatal("pruned payload still in backend")
	}
	metas, _ := List(dir)
	if len(metas) != 2 {
		t.Fatalf("expected 2 left, got %d", len(metas))
	}
}
