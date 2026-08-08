package backup

import (
	"context"
	"testing"
	"time"

	"github.com/webkaz-labs/kagikae/internal/testutil/secrettest"
)

// seed writes n metas with the given ids and reasons (oldest first in the slice).
func seed(t *testing.T, dir string, pairs [][2]string) {
	t.Helper()
	for _, p := range pairs {
		meta := Meta{
			SchemaVersion: 1, ID: p[0], Reason: p[1],
			CreatedAt: time.Now().UTC(), Tools: []string{"claude"},
			ActiveBefore: map[string]string{}, Artifacts: []ArtifactRecord{},
		}
		if err := Save(dir, meta); err != nil {
			t.Fatal(err)
		}
	}
}

func ids(metas []Meta) []string {
	out := []string{}
	for _, m := range metas {
		out = append(out, m.ID)
	}
	return out
}

// The reason here is deliberately **not** kae's own token. Prune takes a predicate
// precisely so this package does not know that vocabulary, and spelling the real reason
// in here would be a third hand-written copy of a rule whose two copies review already
// flagged. An arbitrary string proves the seam rather than the caller's policy.
const uncounted = "not-an-undo-target"

func countsUndoTargets(m Meta) bool { return m.Reason != uncounted }

// Rejected entries interleaved with accepted ones: the cutoff must be the keep-th
// *accepted* backup, and rejects newer than it survive without consuming a slot.
func TestR4PruneInterleaved(t *testing.T) {
	dir := t.TempDir()
	// newest last in this list; List sorts descending so the newest id is 20260611T012400Z
	seed(t, dir, [][2]string{
		{"20260611T012340Z", "run"},     // oldest, expect pruned
		{"20260611T012350Z", uncounted}, // older than cutoff -> pruned
		{"20260611T012355Z", "run"},     // 2nd accepted -> cutoff
		{"20260611T012358Z", uncounted}, // newer than cutoff -> kept, no slot
		{"20260611T012400Z", "switch"},  // 1st accepted
	})
	be := secrettest.NewMem()
	removed, err := Prune(context.Background(), be, dir, 2, countsUndoTargets)
	if err != nil {
		t.Fatal(err)
	}
	left, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("removed=%v left=%v", removed, ids(left))
	want := map[string]bool{
		"20260611T012400Z": true, "20260611T012358Z": true, "20260611T012355Z": true,
	}
	if len(left) != len(want) {
		t.Errorf("expected 3 left (2 undo targets + the newer preserved copy), got %v", ids(left))
	}
	for _, m := range left {
		if !want[m.ID] {
			t.Errorf("unexpected survivor %s (reason %s)", m.ID, m.Reason)
		}
	}
}

// Fewer than `keep` countable backups, so the positional cutoff never fires — the case
// that used to prune nothing at all and therefore had no bound on the rejected side.
//
// **This test asserted that unboundedness until 2026-08-08**, on the stated grounds that
// every declining `run -s` writes one countable backup beside its preserved copy, so one
// always existed to age against. `kae relogin`'s pre-flight refusal broke that: a pin-only
// workflow runs no backup-writing command, so five refusals left five full copies of a
// credential in the secret store with nothing able to remove them. The rejected side now
// carries `keep` on its own, which is what this asserts instead.
func TestR4PruneFewerThanKeepCountable(t *testing.T) {
	dir := t.TempDir()
	pairs := [][2]string{{"20260611T012300Z", "run"}}
	for i := range 40 {
		pairs = append(pairs, [2]string{
			// all newer than the single countable one
			time.Date(2026, 6, 11, 1, 24, i, 0, time.UTC).Format("20060102T150405Z"),
			uncounted,
		})
	}
	seed(t, dir, pairs)
	be := secrettest.NewMem()
	removed, err := Prune(context.Background(), be, dir, 30, countsUndoTargets)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := List(dir)
	t.Logf("removed=%d left=%d", len(removed), len(left))
	// 30 rejected (their own keep) + the single countable one, which the cutoff never
	// reaches. The 10 oldest rejected entries go.
	if len(left) != 31 {
		t.Errorf("expected the rejected side bounded at keep + the countable one, got %d", len(left))
	}
	if len(removed) != 10 {
		t.Errorf("expected the 10 oldest rejected entries pruned, got %d", len(removed))
	}
	// The bound must not have come from the countable one being evicted: an undo target
	// under keep is never pruned, and losing it is the failure the no-slot rule exists for.
	for _, m := range left {
		if m.Reason == "run" {
			goto countableSurvived
		}
	}
	t.Fatalf("the countable backup must survive: %v", ids(left))
countableSurvived:
}

// The padded/unpadded boundary. Old ids (`-2`) and new ones (`-02`) cannot share a
// second in production, so cross-second comparison is what matters — the timestamp
// prefix is fixed width, so it must be correct regardless of suffix width.
func TestR4PruneAcrossThePaddingBoundary(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, [][2]string{
		{"20260611T012345Z", "run"},    // oldest second
		{"20260611T012345Z-2", "run"},  // old-style suffix, same second, later
		{"20260611T012400Z", "run"},    // next second
		{"20260611T012400Z-02", "run"}, // new-style suffix, same second, later
	})
	list, _ := List(dir)
	t.Logf("descending: %v", ids(list))
	if list[0].ID != "20260611T012400Z-02" {
		t.Errorf("newest must be the padded id in the later second, got %s", list[0].ID)
	}
	be := secrettest.NewMem()
	if _, err := Prune(context.Background(), be, dir, 2, countsUndoTargets); err != nil {
		t.Fatal(err)
	}
	left, _ := List(dir)
	t.Logf("left=%v", ids(left))
	if len(left) != 2 || left[0].ID != "20260611T012400Z-02" || left[1].ID != "20260611T012400Z" {
		t.Errorf("the two newest by time must survive: %v", ids(left))
	}
}

// Same second, mixed suffix widths — the one case the padding comment calls unreachable.
// Measured so the claim is on the record rather than assumed.
func TestR4SameSecondMixedSuffixWidths(t *testing.T) {
	dir := t.TempDir()
	seed(t, dir, [][2]string{
		{"20260611T012345Z", "run"},
		{"20260611T012345Z-2", "run"},  // written by the old binary
		{"20260611T012345Z-02", "run"}, // written by the new one, later in time
	})
	list, _ := List(dir)
	t.Logf("descending: %v", ids(list))
	if list[0].ID != "20260611T012345Z-02" {
		t.Logf("MIXED-WIDTH HAZARD: the newest-created id %q is not first; List reports %q",
			"20260611T012345Z-02", list[0].ID)
	}
	// And NewID picks -02 when -2 already occupies the slot, which is how the pair arises.
	if got := NewID(dir, time.Date(2026, 6, 11, 1, 23, 45, 0, time.UTC)); got != "20260611T012345Z-03" {
		t.Logf("NewID in a second holding both widths returned %q", got)
	}
}
