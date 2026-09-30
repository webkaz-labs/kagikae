package main

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func names(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("TestN%03d", i)
	}
	return out
}

func TestPartitionCoversEveryNameOnceForEveryShardCount(t *testing.T) {
	all := names(23)
	for n := 1; n <= 30; n++ {
		shards := partition(all, n)
		if want := min(n, len(all)); len(shards) != want {
			t.Fatalf("n=%d: %d shards, want %d", n, len(shards), want)
		}
		var got []string
		for _, s := range shards {
			if len(s) == 0 {
				t.Fatalf("n=%d: empty shard", n)
			}
			got = append(got, s...)
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, all) {
			t.Fatalf("n=%d: shards do not cover the set exactly once", n)
		}
	}
}

func TestPartitionIsDeterministicAndIgnoresInputOrder(t *testing.T) {
	all := names(10)
	reversed := append([]string(nil), all...)
	sort.Sort(sort.Reverse(sort.StringSlice(reversed)))
	if !reflect.DeepEqual(partition(all, 3), partition(reversed, 3)) {
		t.Fatal("partition depends on input order")
	}
}

func TestShardCount(t *testing.T) {
	for _, tc := range []struct {
		override   string
		cpus, want int
	}{{"", 4, 4}, {"", 32, maxShards}, {"1", 32, 1}, {"12", 4, 12}, {"1000", 4, maxOverride}, {"0", 4, 4}, {"x", 4, 4}, {"", 0, 1}} {
		if got := shardCount(tc.override, tc.cpus); got != tc.want {
			t.Errorf("shardCount(%q, %d) = %d, want %d", tc.override, tc.cpus, got, tc.want)
		}
	}
}

func TestRunPatternQuotesAndAnchors(t *testing.T) {
	if got, want := runPattern([]string{"TestA", "TestB_c"}), "^(TestA|TestB_c)$"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := runPattern([]string{"Test.x"}); got != `^(Test\.x)$` {
		t.Fatalf("metacharacter not quoted: %q", got)
	}
}

func TestStartedTopLevelSkipsSubtestsAndKeepsRepeats(t *testing.T) {
	out := "=== RUN   TestA\n=== RUN   TestA/sub\n--- PASS: TestA (0.00s)\n=== RUN   TestB\n=== RUN   TestB\nnoise === RUN TestC\n"
	if got, want := startedTopLevel(out), []string{"TestA", "TestB", "TestB"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestVerifyAcceptsAnExactSplit(t *testing.T) {
	all := names(7)
	assigned := partition(all, 3)
	if err := verify(all, assigned, assigned); err != nil {
		t.Fatal(err)
	}
}

// The failing controls: each defect shape must be reported by name.
func TestVerifyRejectsEachDefectShape(t *testing.T) {
	all := []string{"TestA", "TestB", "TestC", "TestD"}
	assigned := [][]string{{"TestA", "TestC"}, {"TestB", "TestD"}}
	for _, tc := range []struct {
		name     string
		listed   []string
		assigned [][]string
		started  [][]string
		want     string
	}{
		{"shard skipped a test", all, assigned, [][]string{{"TestA"}, {"TestB", "TestD"}}, "shard 1: TestC never ran"},
		{"shard ran a test twice", all, assigned, [][]string{{"TestA", "TestC", "TestA"}, {"TestB", "TestD"}}, "shard 1: TestA ran 2 times"},
		{"shard ran an unassigned test", all, assigned, [][]string{{"TestA", "TestC", "TestB"}, {"TestB", "TestD"}}, "shard 1: TestB ran but was not assigned"},
		{"split dropped a listed test", all, [][]string{{"TestA", "TestC"}, {"TestB"}}, [][]string{{"TestA", "TestC"}, {"TestB"}}, "union of shards: TestD never ran"},
		{"split repeated a test", all, [][]string{{"TestA", "TestC"}, {"TestB", "TestD", "TestA"}}, [][]string{{"TestA", "TestC"}, {"TestB", "TestD", "TestA"}}, "union of shards: TestA ran 2 times"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := verify(tc.listed, tc.assigned, tc.started)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestWithoutRemovesTheShardedPackageExactlyOnce(t *testing.T) {
	got, err := without([]string{"m/a", "m/internal/cmd", "m/b"}, "m/internal/cmd")
	if err != nil || !reflect.DeepEqual(got, []string{"m/a", "m/b"}) {
		t.Fatalf("got %v, %v", got, err)
	}
	// Failing controls: absent (a workspace or rename) and repeated.
	for _, c := range []struct {
		pkgs []string
		want string
	}{
		{[]string{"m/a", "m/b"}, "is missing from the go list"},
		{[]string{"m/internal/cmd", "m/internal/cmd"}, "is listed 2 times"},
	} {
		if _, err := without(c.pkgs, "m/internal/cmd"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%v: err = %v, want %q", c.pkgs, err, c.want)
		}
	}
}
