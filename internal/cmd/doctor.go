package cmd

import (
	"context"
	"fmt"

	"github.com/webkaz-labs/kagikae/internal/adapter"
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
	opts, _, ok := parseCommon("doctor", flags, false)
	if !ok {
		return constants.ExitUsage
	}
	toolFilter := ""
	switch len(positionals) {
	case 0:
	case 1:
		toolFilter = positionals[0]
		if !constants.IsTool(toolFilter) {
			return usageError("unknown tool %q (tools: claude, codex, gemini, agy)", toolFilter)
		}
	default:
		return usageError("usage: %s doctor [tool] [--json]", toolName)
	}
	app := newApp(opts.ConfigPath)
	return runDoctor(ctx, app, opts, toolFilter)
}

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
		report.Checks = append(report.Checks, adapter.Check{Code: constants.CheckConfigValid,
			Status: constants.StatusError,
			Message: fmt.Sprintf("config %s: %v", app.displayPath(app.ConfigPath), app.ConfigErr)})
	} else {
		report.Checks = append(report.Checks, adapter.Check{Code: constants.CheckConfigValid,
			Status: constants.StatusOK,
			Message: "config: " + app.displayPath(app.ConfigPath)})
		for _, warning := range app.ConfigWarnings {
			report.Checks = append(report.Checks, adapter.Check{Code: constants.CheckConfigValid,
				Status: constants.StatusWarn, Message: warning})
		}
	}

	// secret backend
	be, err := app.secretBackend()
	if err != nil {
		report.SecretBackend = "unavailable"
		report.Checks = append(report.Checks, adapter.Check{Code: constants.CheckSecretBackend,
			Status: constants.StatusError, Message: err.Error()})
	} else {
		report.SecretBackend = be.Name()
		status := constants.StatusOK
		message := "secret backend: " + be.Name()
		if be.Name() == secret.BackendFile {
			status = constants.StatusWarn
			message += " (plaintext file backend; secrets are stored unencrypted)"
		}
		report.Checks = append(report.Checks, adapter.Check{Code: constants.CheckSecretBackend,
			Status: status, Message: message})
	}

	// per tool
	for _, tool := range app.enabledTools() {
		if toolFilter != "" && tool != toolFilter {
			continue
		}
		ad, err := adapter.ForTool(tool)
		if err != nil {
			report.Checks = append(report.Checks, adapter.Check{Tool: tool,
				Code: constants.CheckUnsupported, Status: constants.StatusError, Message: err.Error()})
			continue
		}
		report.Checks = append(report.Checks, ad.Doctor(ctx, app.Env)...)
	}

	for _, check := range report.Checks {
		if check.Status == constants.StatusError {
			report.OK = false
		}
	}
	return report
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
