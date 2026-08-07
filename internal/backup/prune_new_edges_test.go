package backup

import (
	"context"
	"testing"

	"github.com/webkaz-labs/kagikae/internal/testutil/secrettest"
)

// The three edges the positional rewrite added, plus the one it removed a branch for.
func TestR6PruneNewEdges(t *testing.T) {
	cases := []struct {
		name      string
		pairs     [][2]string
		keep      int
		wantLeft  []string
		wantEmpty bool
	}{{
		name:     "keep exceeds len(metas): nothing is old enough to drop",
		pairs:    [][2]string{{"20260611T012340Z", "run"}, {"20260611T012350Z", "run"}},
		keep:     30,
		wantLeft: []string{"20260611T012350Z", "20260611T012340Z"},
	}, {
		name:      "empty dir",
		pairs:     nil,
		keep:      3,
		wantEmpty: true,
	}, {
		// The half my round-4 case did not cover: an uncountable entry *after* the cutoff
		// must be deleted, or preserved copies accumulate forever.
		name: "uncountable after the cutoff is pruned",
		pairs: [][2]string{
			{"20260611T012340Z", uncounted},
			{"20260611T012345Z", uncounted},
			{"20260611T012350Z", "run"},
			{"20260611T012355Z", "run"},
		},
		keep:     2,
		wantLeft: []string{"20260611T012355Z", "20260611T012350Z"},
	}, {
		name: "uncountable on both sides of the cutoff",
		pairs: [][2]string{
			{"20260611T012335Z", uncounted},
			{"20260611T012340Z", "run"},
			{"20260611T012345Z", uncounted},
			{"20260611T012350Z", "run"},
			{"20260611T012355Z", uncounted},
		},
		keep: 2,
		// cutoff is the 2nd countable (…340Z); the uncountable ones newer than it stay,
		// the one older than it goes.
		wantLeft: []string{"20260611T012355Z", "20260611T012350Z", "20260611T012345Z", "20260611T012340Z"},
	}, {
		name:     "keep of 1 keeps only the newest countable and what is newer",
		pairs:    [][2]string{{"20260611T012340Z", "run"}, {"20260611T012350Z", "run"}, {"20260611T012355Z", uncounted}},
		keep:     1,
		wantLeft: []string{"20260611T012355Z", "20260611T012350Z"},
	}, {
		// Not reachable from config (BackupKeep is validated >= 1) but Prune is exported.
		// Safe direction: nothing is deleted, same as before the rewrite.
		name:     "keep of zero deletes nothing",
		pairs:    [][2]string{{"20260611T012340Z", "run"}, {"20260611T012350Z", "run"}},
		keep:     0,
		wantLeft: []string{"20260611T012350Z", "20260611T012340Z"},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.pairs != nil {
				seed(t, dir, tc.pairs)
			}
			removed, err := Prune(context.Background(), secrettest.NewMem(), dir, tc.keep, countsUndoTargets)
			if err != nil {
				t.Fatal(err)
			}
			left, err := List(dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("removed=%v left=%v", removed, ids(left))
			if tc.wantEmpty {
				if len(left) != 0 {
					t.Fatalf("expected nothing, got %v", ids(left))
				}
				return
			}
			got := ids(left)
			if len(got) != len(tc.wantLeft) {
				t.Fatalf("want %v, got %v", tc.wantLeft, got)
			}
			for i := range got {
				if got[i] != tc.wantLeft[i] {
					t.Fatalf("want %v, got %v", tc.wantLeft, got)
				}
			}
		})
	}
}
