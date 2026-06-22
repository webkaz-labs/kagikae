package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/webkaz-labs/kagikae/internal/account"
	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/companion"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/secret"
)

type doctorReport struct {
	SchemaVersion int             `json:"schema_version"`
	OK            bool            `json:"ok"`
	Platform      string          `json:"platform"`
	SecretBackend string          `json:"secret_backend"`
	Checks        []adapter.Check `json:"checks"`
}

func CmdDoctor(ctx context.Context, args []string) int {
	flags, positionals := splitArgs(args)
	opts, ok := parseCommon("doctor", flags, false, nil)
	if !ok {
		return constants.ExitUsage
	}
	toolFilter := ""
	switch len(positionals) {
	case 0:
	case 1:
		toolFilter = positionals[0]
		// Route through the shared validateTool so doctor reuses the one
		// unknown-tool error (with its did-you-mean hint and removed-tool
		// successor message) instead of a divergent copy.
		if err := validateTool(toolFilter); err != nil {
			return finish(opts, err)
		}
	default:
		return usageError("usage: %s doctor [tool] [--json]", toolName)
	}
	app := newApp(opts.ConfigPath)
	return runDoctor(ctx, app, opts, toolFilter)
}

// runDoctor exits 0/1 (health pass/fail) by design, not via the general
// exit-code table — see docs/CLI.md.
func runDoctor(ctx context.Context, app *App, opts commonOpts, toolFilter string) int {
	report := buildDoctor(ctx, app, toolFilter)
	exit := constants.ExitOK
	if !report.OK {
		exit = constants.ExitError
	}
	if opts.Format == formatJSON {
		if code := encodeJSON(report); code != constants.ExitOK {
			return code
		}
		return exit
	}
	printDoctorReport(report, opts)
	return exit
}

func buildDoctor(ctx context.Context, app *App, toolFilter string) *doctorReport {
	report := &doctorReport{
		SchemaVersion: constants.SchemaVersion,
		OK:            true,
		Platform:      app.Env.GOOS,
		Checks:        []adapter.Check{},
	}

	// config
	if app.ConfigErr != nil {
		report.Checks = append(report.Checks, adapter.Check{
			Code:    constants.CheckConfigValid,
			Status:  constants.StatusError,
			Message: fmt.Sprintf("config %s: %v", app.displayPath(app.ConfigPath), app.ConfigErr),
		})
	} else {
		report.Checks = append(report.Checks, adapter.Check{
			Code:    constants.CheckConfigValid,
			Status:  constants.StatusOK,
			Message: "config: " + app.displayPath(app.ConfigPath),
		})
		for _, warning := range app.ConfigWarnings {
			report.Checks = append(report.Checks, adapter.Check{
				Code:   constants.CheckConfigValid,
				Status: constants.StatusWarn, Message: warning,
			})
		}
	}

	// secret backend
	be, err := app.secretBackend()
	if err != nil {
		report.SecretBackend = "unavailable"
		report.Checks = append(report.Checks, adapter.Check{
			Code:   constants.CheckSecretBackend,
			Status: constants.StatusError, Message: err.Error(),
		})
	} else {
		report.SecretBackend = be.Name()
		status := constants.StatusOK
		message := "secret backend: " + be.Name()
		if be.Name() == secret.BackendFile {
			status = constants.StatusWarn
			message += " (plaintext file backend; secrets are stored unencrypted)"
		}
		report.Checks = append(report.Checks, adapter.Check{
			Code:   constants.CheckSecretBackend,
			Status: status, Message: message,
		})
	}

	// per tool
	for _, tool := range app.enabledTools() {
		if toolFilter != "" && tool != toolFilter {
			continue
		}
		ad, err := adapter.ForTool(tool)
		if err != nil {
			report.Checks = append(report.Checks, adapter.Check{
				Tool: tool,
				Code: constants.CheckUnsupported, Status: constants.StatusError, Message: err.Error(),
			})
			continue
		}
		report.Checks = append(report.Checks, ad.Doctor(ctx, app.Env)...)
	}

	// credential health: stale snapshots and orphaned secret items. Reuse the
	// backend resolved above; skip when it is unavailable.
	if err == nil {
		report.Checks = append(report.Checks, app.credentialHealthChecks(ctx, be, toolFilter)...)
	}

	// companion binding health (config-level). Companions are not tools, so
	// these run only for the unfiltered report.
	if err == nil && toolFilter == "" {
		report.Checks = append(report.Checks, app.companionChecks(ctx, be)...)
	}

	// companion live-identity drift (subprocess): the commit-misidentity guard.
	// Unfiltered only, like companionChecks; needs no secret backend, so it runs
	// even when the backend is unavailable.
	if toolFilter == "" {
		report.Checks = append(report.Checks, app.companionDriftChecks(ctx)...)
	}

	for _, check := range report.Checks {
		if check.Status == constants.StatusError {
			report.OK = false
		}
	}
	return report
}

// companionChecks reports companion binding health: a bound token knob with no
// stored secret (the mise exec() lookup would fail at eval time) and a bound
// companion whose CLI is missing from PATH (the binding has no effect). These
// are config-level and deterministic; the live counterpart that compares actual
// git output against the bound identity is companionDriftChecks. The binary
// check is emitted once per companion id even when several profiles bind it.
func (app *App) companionChecks(ctx context.Context, be secret.Backend) []adapter.Check {
	checks := []adapter.Check{}
	binaryChecked := map[string]bool{}
	for _, profileName := range sortedKeys(app.Config.Profiles) {
		companions := app.Config.Profiles[profileName].Companions
		for _, id := range constants.Companions {
			data, bound := companions[id]
			if !bound {
				continue
			}
			spec, ok := companion.For(id)
			if !ok {
				continue
			}
			if !binaryChecked[id] {
				binaryChecked[id] = true
				if _, err := app.Env.LookPath(spec.Binary); err != nil {
					checks = append(checks, adapter.Check{
						Tool: id, Code: constants.CheckCompanionBinary, Status: constants.StatusWarn,
						Message: spec.Binary + " not found in PATH; the " + id + " binding has no effect until it is installed",
					})
				}
			}
			if spec.Secret() {
				for _, knob := range sortedKeys(data) {
					if _, found, gerr := be.Get(ctx, companion.SecretRef(profileName, id, knob)); gerr == nil && !found {
						checks = append(checks, adapter.Check{
							Tool: id, Code: constants.CheckCompanionMissing, Status: constants.StatusWarn,
							Message: fmt.Sprintf("profile %s: %s token %s is not stored; run: kae companion add %s %s %s",
								profileName, id, knob, profileName, id, knob),
						})
					}
				}
			}
		}
	}
	return checks
}

// credentialHealthChecks surfaces credential staleness the switch path only
// warns about inline (docs/RELEASE.md §D): expired snapshots reusing §B's
// predicate, plus orphaned secret items where the backend can enumerate.
func (app *App) credentialHealthChecks(ctx context.Context, be secret.Backend, toolFilter string) []adapter.Check {
	checks := []adapter.Check{}
	accounts, err := account.List(app.Paths.AccountsDir())
	if err == nil {
		for _, acc := range accounts {
			if toolFilter != "" && acc.Tool != toolFilter {
				continue
			}
			info, err := app.accountFreshness(ctx, be, acc)
			if err != nil || !snapshotExpired(info, app.Now()) || info.HasRefresh {
				continue
			}
			checks = append(checks, adapter.Check{
				Tool: acc.Tool, Code: constants.CheckCredentialStale,
				Status: constants.StatusWarn,
				Message: fmt.Sprintf("snapshot %q expired %s with no refresh token; re-capture with kae add --no-login %s %s",
					acc.Name, info.ExpiresAt.UTC().Format(time.RFC3339), acc.Tool, acc.Name),
			})
		}
	}
	return append(checks, app.orphanChecks(ctx, be, toolFilter)...)
}

// orphanChecks warns when a stored secret item has no matching snapshot dir (a
// leftover from manual cleanup). It runs only where the backend can enumerate
// (file readdir, Linux libsecret); the darwin keychain cannot list by service,
// so the check is silently skipped there (documented gap; docs/SECURITY.md).
func (app *App) orphanChecks(ctx context.Context, be secret.Backend, toolFilter string) []adapter.Check {
	enum, ok := be.(secret.Enumerator)
	if !ok {
		return nil
	}
	keys, err := enum.Keys(ctx)
	if err != nil {
		return nil // best-effort: orphan detection never fails doctor
	}
	checks := []adapter.Check{}
	seen := map[string]bool{}
	for _, key := range keys {
		parts := strings.Split(key, "/")
		if len(parts) < 3 || parts[0] == "backup" {
			continue // not an account ref (backup/<id>/... or malformed)
		}
		tool, acct := parts[0], parts[1]
		id := tool + "/" + acct
		if seen[id] {
			continue
		}
		seen[id] = true
		if toolFilter != "" && tool != toolFilter {
			continue
		}
		if _, found, err := account.Load(app.Paths.AccountDir(tool, acct)); err == nil && found {
			continue
		}
		checks = append(checks, adapter.Check{
			Tool: tool, Code: constants.CheckSecretOrphan,
			Status: constants.StatusWarn,
			Message: fmt.Sprintf("secret item for %s/%s has no snapshot dir; remove it with kae account rm %s %s",
				tool, acct, tool, acct),
		})
	}
	return checks
}

func printDoctorReport(report *doctorReport, opts commonOpts) {
	color := colorEnabled(opts.NoColor)
	fmt.Printf("platform: %s, secret backend: %s\n\n", report.Platform, report.SecretBackend)
	for _, check := range report.Checks {
		label := paint(check.Status, fmt.Sprintf("[%s]", check.Status), color)
		if check.Tool != "" {
			fmt.Printf("%s %s: %s\n", label, check.Tool, check.Message)
		} else {
			fmt.Printf("%s %s\n", label, check.Message)
		}
	}
	if report.OK {
		fmt.Println("\nno blocking problems found")
	} else {
		fmt.Println("\nerrors found; fix them before switching")
	}
}
