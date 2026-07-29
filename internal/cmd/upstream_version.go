package cmd

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

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

// upstreamVersionChecks warns for each installed tool that is a newer
// major/minor than the version its adapter's behaviour assumptions were verified
// against (adapter.VersionVerifier). It is the companion to identityDriftChecks:
// that one catches an assumption already broken, this one flags the release where
// one *could* have broken, before a silent switch failure.
//
// Offline: `<binary> --version` through the runner seam, no network. A tool that
// is not installed, declares no verified version, or prints nothing parseable is
// skipped — a false warning here would train the user to ignore a real one.
func (app *App) upstreamVersionChecks(ctx context.Context, toolFilter string) []adapter.Check {
	checks := []adapter.Check{}
	for _, tool := range app.enabledTools() {
		if toolFilter != "" && tool != toolFilter {
			continue
		}
		ad, err := adapter.ForTool(tool)
		if err != nil {
			continue
		}
		verifier, ok := ad.(adapter.VersionVerifier)
		if !ok {
			continue
		}
		if _, err := app.Env.LookPath(ad.Binary()); err != nil {
			continue // binary_present already reports a missing CLI
		}
		stdout, _, code := runner.Run(ctx, ad.Binary(), "--version")
		if code != 0 {
			continue
		}
		if check, ok := upstreamVersionCheck(tool, stdout, verifier.VerifiedVersion()); ok {
			checks = append(checks, check)
		}
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
