package cmd

import (
	"context"
	"sync"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
)

// upstreamVersionProbeDeadline bounds the whole probe round. `--version` is
// *assumed* offline, but that is a property of six third-party binaries, not of
// kae (copilot's already prints "Run 'copilot update' to check for updates."), so
// the deadline turns the assumption into something kae enforces: doctor cannot
// hang on a tool that decides to phone home or wedge. A var so a test can shrink
// it. exec.CommandContext kills the probe when it fires, and a killed probe is a
// non-zero exit, i.e. the same silent skip as any other failing `--version`.
var upstreamVersionProbeDeadline = 5 * time.Second

// roundProbe is one subprocess probe of doctorProbeRound. ok=false means no
// finding.
type roundProbe func(ctx context.Context) (adapter.Check, bool)

// doctorProbeRound runs every doctor probe that launches an upstream AI CLI in
// one concurrent round under upstreamVersionProbeDeadline, and returns the
// findings of each check: upstream_version's `--version` probes (version) and,
// when residentDaemonVersionEnabled, resident_drift's `daemon version` half
// (resident).
func (app *App) doctorProbeRound(ctx context.Context, toolFilter string) (version, resident []adapter.Check) {
	tools := []string{}
	for _, tool := range app.enabledTools() {
		if toolFilter == "" || tool == toolFilter {
			tools = append(tools, tool)
		}
	}
	found := runProbeRound(ctx, app.upstreamVersionProbes(tools), app.daemonVersionProbes(tools))
	return found[0], found[1]
}

// runProbeRound runs every non-nil probe of every group concurrently under
// upstreamVersionProbeDeadline and returns each group's findings in the order of
// its probes. Each goroutine writes its own slot, so the findings stay in
// canonical tool order without a lock, exactly as detectTools does.
func runProbeRound(ctx context.Context, groups ...[]roundProbe) [][]adapter.Check {
	ctx, cancel := context.WithTimeout(ctx, upstreamVersionProbeDeadline)
	defer cancel()

	slots := make([][]adapter.Check, len(groups))
	var wg sync.WaitGroup
	for g, probes := range groups {
		slots[g] = make([]adapter.Check, len(probes))
		for i, probe := range probes {
			if probe == nil {
				continue
			}
			wg.Go(func() {
				if check, ok := probe(ctx); ok {
					slots[g][i] = check
				}
			})
		}
	}
	wg.Wait()

	found := make([][]adapter.Check, len(groups))
	for g, slot := range slots {
		found[g] = []adapter.Check{}
		for _, check := range slot {
			if check.Code != "" { // a skipped or silent probe left its slot zeroed
				found[g] = append(found[g], check)
			}
		}
	}
	return found
}
