package cmd

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/runner"
)

// versionTripleRe matches the first <major>.<minor>.<patch> triple of a
// `--version` output. Every upstream CLI kae drives prints one, none of them in
// the same place: "2.1.220 (Claude Code)", "codex-cli 0.145.0", bare "1.17.4",
// "GitHub Copilot CLI 1.0.61." (trailing period), and cursor-agent's date
// version "2026.06.16-20-30-07-<sha>". Taking the leftmost triple reads all of
// them; TestParseUpstreamVersion pins the real outputs.
var versionTripleRe = regexp.MustCompile(`([0-9]+)\.([0-9]+)\.([0-9]+)`)

// upstreamVersion is a parsed major.minor.patch triple.
type upstreamVersion struct{ major, minor, patch int }

// parseUpstreamVersion extracts the first version triple from s. raw is the
// matched text, kept for display so a zero-padded field (cursor's "06") is shown
// as printed. ok=false when s carries no triple.
func parseUpstreamVersion(s string) (raw string, v upstreamVersion, ok bool) {
	m := versionTripleRe.FindStringSubmatch(s)
	if m == nil {
		return "", upstreamVersion{}, false
	}
	for i, field := range []*int{&v.major, &v.minor, &v.patch} {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return "", upstreamVersion{}, false // digit run too long for an int
		}
		*field = n
	}
	return m[0], v, true
}

// movedPast reports whether v is a newer major or minor than verified. A
// patch-only bump is deliberately silent: claude ships patches weekly, so
// warning on each would be noise long before it was ever signal. An older
// installed version is fine too — the assumptions were verified above it.
func (v upstreamVersion) movedPast(verified upstreamVersion) bool {
	if v.major != verified.major {
		return v.major > verified.major
	}
	return v.minor > verified.minor
}

// upstreamVersionProbeDeadline bounds the whole probe round. `--version` is
// *assumed* offline, but that is a property of six third-party binaries, not of
// kae (copilot's already prints "Run 'copilot update' to check for updates."), so
// the deadline turns the assumption into something kae enforces: doctor cannot
// hang on a tool that decides to phone home or wedge. A var so a test can shrink
// it. exec.CommandContext kills the probe when it fires, and a killed probe is a
// non-zero exit, i.e. the same silent skip as any other failing `--version`.
var upstreamVersionProbeDeadline = 5 * time.Second

// upstreamVersionChecks warns for each installed tool that is a newer
// major/minor than the version its adapter's behaviour assumptions were verified
// against (adapter.VerifiedVersion). It is the companion to identityDriftChecks:
// that one catches an assumption already broken, this one flags the release where
// one *could* have broken, before a silent switch failure.
//
// `<binary> --version` through the runner seam, one process per installed tool.
// The probes run concurrently under upstreamVersionProbeDeadline: they are
// independent process spawns, and serially they dominated doctor's runtime (2.9s
// vs 1.3s for the whole command, six tools installed, against a doctor that
// launched no upstream CLI at all before this check existed). Each goroutine
// writes its own index, so the findings stay in canonical tool order without a
// lock, exactly as detectTools does.
//
// A tool that is not installed, declares no verified version, or prints nothing
// parseable is skipped — a false warning here would train the user to ignore a
// real one.
func (app *App) upstreamVersionChecks(ctx context.Context, toolFilter string) []adapter.Check {
	ctx, cancel := context.WithTimeout(ctx, upstreamVersionProbeDeadline)
	defer cancel()

	tools := []string{}
	for _, tool := range app.enabledTools() {
		if toolFilter == "" || tool == toolFilter {
			tools = append(tools, tool)
		}
	}
	found := make([]adapter.Check, len(tools))
	var wg sync.WaitGroup
	for i, tool := range tools {
		ad, err := adapter.ForTool(tool)
		if err != nil {
			continue
		}
		verified := ad.VerifiedVersion()
		if verified == "" {
			continue // declares no usable signal (cursor): don't even spawn the probe
		}
		if _, err := app.Env.LookPath(ad.Binary()); err != nil {
			continue // binary_present already reports a missing CLI
		}
		wg.Add(1)
		go func(i int, tool, verified string, binary string) {
			defer wg.Done()
			stdout, _, code := runner.Run(ctx, binary, "--version")
			if code != 0 {
				return
			}
			if check, ok := upstreamVersionCheck(tool, stdout, verified); ok {
				found[i] = check
			}
		}(i, tool, verified, ad.Binary())
	}
	wg.Wait()

	checks := []adapter.Check{}
	for _, check := range found {
		if check.Code != "" { // a skipped or silent tool left its slot zeroed
			checks = append(checks, check)
		}
	}
	return checks
}

// assumptionMaxAge is how long a tool's behaviour assumptions may go unchecked
// before doctor says so. Six months, not one or three: upstreamVersionChecks
// already covers the case where the tool actually moved, so this one exists for
// the case where nothing did — and a reminder that fires while the answer is
// still right is the kind users learn to scroll past, which would cost the real
// warnings their credibility too.
const assumptionMaxAge = 180 * 24 * time.Hour

// assumptionAgeChecks reports tools whose behaviour assumptions have gone
// unchecked for assumptionMaxAge. It is the half upstreamVersionChecks cannot
// cover: that one only fires when the installed tool moves past the verified
// release, so **a user who never upgrades gets no signal at all** — and cursor,
// whose date versions make the comparison useless, would get none ever.
//
// Offline and instant: it reads a date the adapter declares, spawns nothing, and
// does not care whether the tool is installed. It reuses the upstream_version
// check code rather than adding one, because the finding and the remedy are the
// same — re-verify the rows and re-record.
func (app *App) assumptionAgeChecks(toolFilter string) []adapter.Check {
	checks := []adapter.Check{}
	for _, tool := range app.enabledTools() {
		if toolFilter != "" && tool != toolFilter {
			continue
		}
		ad, err := adapter.ForTool(tool)
		if err != nil {
			continue
		}
		verifiedOn, err := time.Parse(time.DateOnly, ad.VerifiedOn())
		if err != nil {
			// A value the age check cannot read must say so rather than skip in
			// silence: a typo here would otherwise read as "nothing to report",
			// which is the failure mode the upstream_version parser already has.
			checks = append(checks, adapter.Check{
				Tool: tool, Code: constants.CheckUpstreamVersion, Status: constants.StatusWarn,
				Message: fmt.Sprintf(
					"kae cannot read when %s's behaviour assumptions were last verified (%q is not YYYY-MM-DD), so their age is unknown",
					tool, ad.VerifiedOn(),
				),
			})
			continue
		}
		age := app.Now().UTC().Sub(verifiedOn)
		if age < assumptionMaxAge {
			continue
		}
		checks = append(checks, adapter.Check{
			Tool: tool, Code: constants.CheckUpstreamVersion, Status: constants.StatusWarn,
			Message: fmt.Sprintf(
				"kae's %s behaviour assumptions were last verified on %s (%d days ago) and nothing has re-checked them since; the version signal only fires when %s is upgraded, so re-verify the rows in docs/VALIDATION.md \"Upstream Behaviour Assumptions\"",
				tool, ad.VerifiedOn(), int(age.Hours()/24), tool,
			),
		})
	}
	return checks
}

// upstreamVersionCheck compares one tool's `--version` output against the
// version its behaviour assumptions were verified on. ok=false means no finding:
// the installed version is not past the verified one, or one of the two is
// unparseable (no evidence either way).
func upstreamVersionCheck(tool, output, verified string) (adapter.Check, bool) {
	rawLive, live, okLive := parseUpstreamVersion(output)
	_, want, okWant := parseUpstreamVersion(verified)
	if !okLive || !okWant || !live.movedPast(want) {
		return adapter.Check{}, false
	}
	return adapter.Check{
		Tool: tool, Code: constants.CheckUpstreamVersion, Status: constants.StatusWarn,
		Message: fmt.Sprintf(
			"installed %s %s is past %s, the version kae's behaviour assumptions were last verified against; the layout guards still pass when only the behaviour changed (a field the tool stops maintaining, a cache it stops refreshing), so re-verify the assumptions in docs/VALIDATION.md \"Upstream Behaviour Assumptions\"",
			tool, rawLive, verified,
		),
	}, true
}
