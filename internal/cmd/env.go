package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/envprofile"
)

// CmdEnv manages env-mode profiles (variable names in metadata, values in
// the secret backend):
//
//	kae env set <tool> <account> KEY=VALUE...   (or a single KEY, value from stdin)
//	kae env unset <tool> <account> [KEY...]
//	kae env list [--json]
func CmdEnv(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return usageError("usage: %s env <set|unset|list> ...", toolName)
	}
	sub, rest := args[0], args[1:]
	flags, positionals := splitArgs(rest)
	opts, ok := parseCommon("env "+sub, flags, false, nil)
	if !ok {
		return constants.ExitUsage
	}
	app := newApp(opts.ConfigPath)
	switch sub {
	case "set":
		return runEnvSet(ctx, app, opts, positionals)
	case "unset":
		return runEnvUnset(ctx, app, opts, positionals)
	case "list":
		if len(positionals) != 0 {
			return usageError("usage: %s env list [--json]", toolName)
		}
		return runEnvList(ctx, app, opts)
	default:
		return usageError("unknown env subcommand: %s (set, unset, list)", sub)
	}
}

// parseEnvAssignments turns KEY=VALUE arguments into a map. A single bare
// KEY reads its value from stdin so secrets can bypass argv/shell history.
func parseEnvAssignments(positionals []string, stdin io.Reader) (map[string]string, error) {
	values := map[string]string{}
	bare := []string{}
	for _, arg := range positionals {
		key, value, hasValue := strings.Cut(arg, "=")
		if !envprofile.ValidVarName(key) {
			return nil, errf(constants.ExitUsage, "invalid environment variable name %q", key)
		}
		if hasValue {
			values[key] = value
		} else {
			bare = append(bare, key)
		}
	}
	switch {
	case len(bare) == 0:
	case len(bare) == 1 && len(values) == 0:
		data, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("read value from stdin: %w", err)
		}
		value := strings.TrimRight(string(data), "\r\n")
		if value == "" {
			return nil, errf(constants.ExitUsage, "no value on stdin for %s", bare[0])
		}
		values[bare[0]] = value
	default:
		return nil, errf(constants.ExitUsage,
			"mix of KEY=VALUE and bare KEY is not supported; pass one bare KEY alone to read its value from stdin")
	}
	if len(values) == 0 {
		return nil, errf(constants.ExitUsage, "no variables given")
	}
	return values, nil
}

func runEnvSet(ctx context.Context, app *App, opts commonOpts, positionals []string) int {
	if len(positionals) < 3 {
		return usageError("usage: %s env set <tool> <account> KEY=VALUE... (or one bare KEY with the value on stdin)", toolName)
	}
	tool, accountName := positionals[0], positionals[1]
	if err := validateToolAccount(tool, accountName, "account"); err != nil {
		return finish(opts, err)
	}
	if err := app.requireConfig(); err != nil {
		return finish(opts, err)
	}
	values, err := parseEnvAssignments(positionals[2:], os.Stdin)
	if err != nil {
		return finish(opts, err)
	}
	be, err := app.secretBackend()
	if err != nil {
		return finish(opts, err)
	}
	dir := app.Paths.EnvProfileDir(tool, accountName)
	profile, _, err := envprofile.Load(dir)
	if err != nil {
		return finish(opts, err)
	}
	profile.Version = 1
	profile.Tool = tool
	profile.Account = accountName
	profile.UpdatedAt = app.Now().UTC()
	existing := map[string]bool{}
	for _, name := range profile.Vars {
		existing[name] = true
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := be.Set(ctx, envprofile.SecretRef(tool, accountName, name), []byte(values[name])); err != nil {
			return finish(opts, fmt.Errorf("store %s: %w", name, err))
		}
		if !existing[name] {
			profile.Vars = append(profile.Vars, name)
		}
	}
	if err := envprofile.Save(dir, profile); err != nil {
		return finish(opts, err)
	}
	fmt.Printf("Stored %d variable(s) in env profile %s/%s: %s\n",
		len(names), tool, accountName, strings.Join(names, ", "))
	return constants.ExitOK
}

func runEnvUnset(ctx context.Context, app *App, opts commonOpts, positionals []string) int {
	if len(positionals) < 2 {
		return usageError("usage: %s env unset <tool> <account> [KEY...]", toolName)
	}
	tool, accountName := positionals[0], positionals[1]
	if err := validateToolAccount(tool, accountName, "account"); err != nil {
		return finish(opts, err)
	}
	if err := app.requireConfig(); err != nil {
		return finish(opts, err)
	}
	be, err := app.secretBackend()
	if err != nil {
		return finish(opts, err)
	}
	dir := app.Paths.EnvProfileDir(tool, accountName)
	profile, found, err := envprofile.Load(dir)
	if err != nil {
		return finish(opts, err)
	}
	if !found {
		return finish(opts, errf(constants.ExitNotFound, "env profile %s/%s does not exist", tool, accountName))
	}
	keys := positionals[2:]
	if len(keys) == 0 {
		if err := envprofile.Delete(ctx, be, dir, profile); err != nil {
			return finish(opts, err)
		}
		fmt.Printf("Deleted env profile %s/%s\n", tool, accountName)
		return constants.ExitOK
	}
	remaining := []string{}
	remove := map[string]bool{}
	for _, key := range keys {
		remove[key] = true
	}
	for _, name := range profile.Vars {
		if remove[name] {
			if err := be.Delete(ctx, envprofile.SecretRef(tool, accountName, name)); err != nil {
				return finish(opts, fmt.Errorf("delete %s: %w", name, err))
			}
		} else {
			remaining = append(remaining, name)
		}
	}
	profile.Vars = remaining
	profile.UpdatedAt = app.Now().UTC()
	if len(remaining) == 0 {
		if err := envprofile.Delete(ctx, be, dir, profile); err != nil {
			return finish(opts, err)
		}
		fmt.Printf("Deleted env profile %s/%s\n", tool, accountName)
		return constants.ExitOK
	}
	if err := envprofile.Save(dir, profile); err != nil {
		return finish(opts, err)
	}
	fmt.Printf("Removed %d variable(s) from env profile %s/%s\n", len(keys), tool, accountName)
	return constants.ExitOK
}

type envProfileItem struct {
	Tool      string   `json:"tool"`
	Account   string   `json:"account"`
	Vars      []string `json:"vars"`
	UpdatedAt string   `json:"updated_at"`
}

type envListReport struct {
	SchemaVersion int              `json:"schema_version"`
	Profiles      []envProfileItem `json:"profiles"`
}

func runEnvList(_ context.Context, app *App, opts commonOpts) int {
	if err := app.requireConfig(); err != nil {
		return finish(opts, err)
	}
	profiles, err := envprofile.List(app.Paths.EnvProfilesDir())
	if err != nil {
		return finish(opts, err)
	}
	report := envListReport{SchemaVersion: constants.SchemaVersion, Profiles: []envProfileItem{}}
	for _, profile := range profiles {
		vars := profile.Vars
		if vars == nil {
			vars = []string{}
		}
		report.Profiles = append(report.Profiles, envProfileItem{
			Tool: profile.Tool, Account: profile.Account, Vars: vars,
			UpdatedAt: profile.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	if len(report.Profiles) == 0 {
		fmt.Println("no env profiles (create one with: kae env set <tool> <account> KEY=VALUE)")
		return constants.ExitOK
	}
	rows := [][]string{}
	for _, item := range report.Profiles {
		rows = append(rows, []string{item.Tool, item.Account, strings.Join(item.Vars, ", ")})
	}
	printTable([]string{"Tool", "Account", "Variables"}, rows)
	return constants.ExitOK
}
