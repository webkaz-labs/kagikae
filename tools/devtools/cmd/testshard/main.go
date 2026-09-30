// Command testshard runs the repository's Go tests with the slowest package split
// across processes.
//
// internal/cmd holds most of the suite's wall time and its tests swap process
// globals (working directory, os.Stdout, environment), so they cannot share a
// process. testshard lists that package's top-level tests, deals them round-robin
// by sorted name into shards, runs each shard as its own `go test -run` process
// and runs every other package with one plain `go test` beside them. Because each
// shard is a real `go test` invocation, the test cache and -count behave as they do
// for `go test ./...`.
//
// A split that dropped or repeated a test would pass without saying so, so every
// shard's own verbose output is checked: the top-level tests it started must be
// exactly the ones it was given, each once, and the shards together must cover the
// listed set. Any mismatch fails the run.
//
// Usage, from the module root:
//
//	go run ./tools/devtools/cmd/testshard [-count1]
//
// KAE_TEST_SHARDS sets the shard count (default: CPU count, at most 6; 1 runs the
// package in one process). -count1 passes -count=1 to every go test. The exit
// status is 1 when any test fails or the split does not verify.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/webkaz-labs/kagikae/tools/devtools/commandrun"
)

const (
	shardedPackage = "./internal/cmd"
	shardsEnv      = "KAE_TEST_SHARDS"
	maxShards      = 6
	runTimeout     = 30 * time.Minute
)

var (
	topLevelName = regexp.MustCompile(`^(Test|Example|Fuzz)`)
	runLine      = regexp.MustCompile(`(?m)^=== RUN +(\S+)$`)
)

func main() {
	count1 := flag.Bool("count1", false, "pass -count=1 to every go test")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	code, err := run(ctx, os.Stdout, os.Stderr, shardCount(os.Getenv(shardsEnv), runtime.NumCPU()), *count1)
	if err != nil {
		fmt.Fprintf(os.Stderr, "testshard: %v\n", err)
		code = 1
	}
	os.Exit(code)
}

// shardCount reads the override; an unusable value falls back to the CPU count.
func shardCount(override string, cpus int) int {
	if n, err := strconv.Atoi(override); err == nil && n >= 1 {
		return n
	}
	return max(1, min(cpus, maxShards))
}

// partition deals names round-robin, in sorted order, into at most n non-empty
// shards. Related tests are adjacent by name and often cost alike, so dealing
// spreads them.
func partition(names []string, n int) [][]string {
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	n = max(1, min(n, len(sorted)))
	shards := make([][]string, n)
	for i, name := range sorted {
		shards[i%n] = append(shards[i%n], name)
	}
	return shards
}

// runPattern selects exactly the given top-level tests. Subtests match by their
// parent because the pattern has no slash.
func runPattern(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = regexp.QuoteMeta(name)
	}
	return "^(" + strings.Join(quoted, "|") + ")$"
}

// startedTopLevel returns every top-level test a verbose run started, in order.
func startedTopLevel(output string) []string {
	var started []string
	for _, m := range runLine.FindAllStringSubmatch(output, -1) {
		if !strings.Contains(m[1], "/") {
			started = append(started, m[1])
		}
	}
	return started
}

// verify fails unless the shards together hold exactly the listed tests once each
// and every shard started exactly the tests it was given, once each.
func verify(listed []string, assigned, started [][]string) error {
	var problems []string
	for i, names := range assigned {
		problems = append(problems, diff(fmt.Sprintf("shard %d", i+1), names, started[i])...)
	}
	problems = append(problems, diff("union of shards", listed, flatten(assigned))...)
	if len(problems) > 0 {
		return errors.New("split does not verify:\n  " + strings.Join(problems, "\n  "))
	}
	return nil
}

// diff names what expected has that got lacks, what got has that expected lacks,
// and what got repeats.
func diff(label string, expected, got []string) []string {
	count := func(names []string) map[string]int {
		m := map[string]int{}
		for _, name := range names {
			m[name]++
		}
		return m
	}
	e, g := count(expected), count(got)
	var out []string
	for _, name := range sortedKeys(e) {
		if g[name] == 0 {
			out = append(out, fmt.Sprintf("%s: %s never ran", label, name))
		}
	}
	for _, name := range sortedKeys(g) {
		switch {
		case e[name] == 0:
			out = append(out, fmt.Sprintf("%s: %s ran but was not assigned", label, name))
		case g[name] > 1:
			out = append(out, fmt.Sprintf("%s: %s ran %d times", label, name, g[name]))
		}
	}
	return out
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func flatten(shards [][]string) []string {
	var all []string
	for _, s := range shards {
		all = append(all, s...)
	}
	return all
}

// listTests returns the package's top-level tests. Benchmarks are listed by
// `go test -list` too but never run without -bench, so they are filtered out.
func listTests(ctx context.Context, count1 bool) ([]string, error) {
	res, err := goTest(ctx, count1, "-list", ".*", shardedPackage)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("listing %s failed:\n%s%s", shardedPackage, res.Stdout, res.Stderr)
	}
	var names []string
	for _, line := range strings.Split(res.Stdout, "\n") {
		if topLevelName.MatchString(strings.TrimSpace(line)) && !strings.ContainsAny(strings.TrimSpace(line), " \t") {
			names = append(names, strings.TrimSpace(line))
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("listing %s found no tests:\n%s%s", shardedPackage, res.Stdout, res.Stderr)
	}
	return names, nil
}

func goTest(ctx context.Context, count1 bool, args ...string) (commandrun.Result, error) {
	full := []string{"test"}
	if count1 {
		full = append(full, "-count=1")
	}
	return commandrun.Command{Name: "go", Args: append(full, args...), Timeout: runTimeout}.Run(ctx)
}

// otherPackages lists every package except the sharded one.
func otherPackages(ctx context.Context) ([]string, error) {
	res, err := commandrun.Command{Name: "go", Args: []string{"list", "./..."}, Timeout: time.Minute}.Run(ctx)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("go list ./... failed:\n%s%s", res.Stdout, res.Stderr)
	}
	mod, err := commandrun.Command{Name: "go", Args: []string{"list", "-m"}, Timeout: time.Minute}.Run(ctx)
	if err != nil || mod.ExitCode != 0 {
		return nil, fmt.Errorf("go list -m failed: %v %s", err, mod.Stderr)
	}
	skip := strings.TrimSpace(mod.Stdout) + strings.TrimPrefix(shardedPackage, ".")
	var pkgs []string
	for _, p := range strings.Fields(res.Stdout) {
		if p != skip {
			pkgs = append(pkgs, p)
		}
	}
	return pkgs, nil
}

type outcome struct {
	label   string
	res     commandrun.Result
	err     error
	elapsed time.Duration
}

func (o outcome) failed() bool { return o.err != nil || o.res.ExitCode != 0 }

func run(ctx context.Context, stdout, stderr *os.File, shards int, count1 bool) (int, error) {
	start := time.Now()
	names, err := listTests(ctx, count1)
	if err != nil {
		return 1, err
	}
	assigned := partition(names, shards)
	others, err := otherPackages(ctx)
	if err != nil {
		return 1, err
	}

	shardResults := make([]outcome, len(assigned))
	var otherResult outcome
	var wg sync.WaitGroup
	if len(others) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t0 := time.Now()
			res, err := goTest(ctx, count1, others...)
			otherResult = outcome{label: "other packages", res: res, err: err, elapsed: time.Since(t0)}
		}()
	}
	for i, shard := range assigned {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t0 := time.Now()
			res, err := goTest(ctx, count1, "-v", "-run", runPattern(shard), shardedPackage)
			shardResults[i] = outcome{label: fmt.Sprintf("shard %d/%d", i+1, len(assigned)), res: res, err: err, elapsed: time.Since(t0)}
		}()
	}
	wg.Wait()

	failed := false
	report := func(o outcome, detail bool) {
		if o.failed() {
			failed = true
			fmt.Fprintf(stdout, "FAIL %s (%s)\n", o.label, o.elapsed.Round(time.Millisecond))
			fmt.Fprint(stdout, o.res.Stdout)
			fmt.Fprint(stderr, o.res.Stderr)
			if o.err != nil {
				fmt.Fprintf(stderr, "%s: %v\n", o.label, o.err)
			}
			return
		}
		if detail {
			fmt.Fprint(stdout, o.res.Stdout)
		}
	}
	if len(others) > 0 {
		report(otherResult, true)
	}
	started := make([][]string, len(assigned))
	for i, o := range shardResults {
		report(o, false)
		started[i] = startedTopLevel(o.res.Stdout)
		if !o.failed() {
			fmt.Fprintf(stdout, "ok   %s: %d tests (%s)\n", o.label, len(assigned[i]), o.elapsed.Round(time.Millisecond))
		}
	}
	// A failed shard may have stopped early (a panic or -failfast), so its started
	// set is only comparable when every shard ended normally.
	if !failed {
		if err := verify(names, assigned, started); err != nil {
			return 1, err
		}
		fmt.Fprintf(stdout, "ok   %d %s tests in %d shards, each run once (%s)\n",
			len(names), shardedPackage, len(assigned), time.Since(start).Round(time.Millisecond))
		return 0, nil
	}
	return 1, nil
}
