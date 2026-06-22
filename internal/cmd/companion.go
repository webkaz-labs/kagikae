package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/companion"
	"github.com/webkaz-labs/kagikae/internal/config"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// CmdCompanion manages a profile's companion-auth bindings (git/gh/cloud CLIs):
//
//	kae companion add <profile> <id> KEY=VALUE...   (non-secret knobs: git identity, config paths)
//	kae companion add <profile> <id> KEY            (one secret/token knob; value read from stdin)
//	kae companion rm  <profile> <id> [KEY...]       (drop knobs, or the whole companion)
//	kae companion list [--json]
//
// Secret (token) knob values are stored in the secret backend under
// companion.SecretRef and only a "" marker is written to config.toml; non-secret
// knobs (git identity, config-dir paths) are written inline. See
// docs/ADAPTERS-COMPANION.md.
func CmdCompanion(ctx context.Context, args []string) int {
	if len(args) == 0 {
		return usageError("usage: %s companion <add|rm|list> ...", toolName)
	}
	sub, rest := args[0], args[1:]
	flags, positionals := splitArgs(rest)
	opts, ok := parseCommon("companion "+sub, flags, false, nil)
	if !ok {
		return constants.ExitUsage
	}
	app := newApp(opts.ConfigPath)
	switch sub {
	case "add":
		return runCompanionAdd(ctx, app, opts, positionals)
	case "rm":
		return runCompanionRm(ctx, app, opts, positionals)
	case "list":
		if len(positionals) != 0 {
			return usageError("usage: %s companion list [--json]", toolName)
		}
		return runCompanionList(ctx, app, opts)
	default:
		return usageError("unknown companion subcommand: %s (add, rm, list)", sub)
	}
}

// resolveCompanionTarget validates the profile/companion pair and returns the
// spec. The profile must already be defined (companions bind to an existing
// profile, not create one).
func (app *App) resolveCompanionTarget(profileName, id string) (companion.Spec, error) {
	if err := app.requireConfigFile(); err != nil {
		return companion.Spec{}, err
	}
	if _, ok := app.Config.Profiles[profileName]; !ok {
		return companion.Spec{}, errf(constants.ExitNotFound,
			"profile %q is not defined (create it first, e.g. kae profile set %s <tool> <account>)", profileName, profileName)
	}
	spec, ok := companion.For(id)
	if !ok {
		return companion.Spec{}, errf(constants.ExitUsage,
			"unknown companion %q (known: %s)", id, strings.Join(constants.Companions, ", "))
	}
	return spec, nil
}

func runCompanionAdd(ctx context.Context, app *App, opts commonOpts, positionals []string) int {
	if len(positionals) < 3 {
		return usageError("usage: %s companion add <profile> <id> KEY=VALUE... (or one bare KEY for a token, value on stdin)", toolName)
	}
	profileName, id := positionals[0], positionals[1]
	spec, err := app.resolveCompanionTarget(profileName, id)
	if err != nil {
		return finish(opts, err)
	}
	inline, secretKnob, err := parseCompanionKnobs(spec, positionals[2:], os.Stdin)
	if err != nil {
		return finish(opts, err)
	}

	cfgLock, err := app.acquireConfigLock()
	if err != nil {
		return finish(opts, err)
	}
	defer cfgLock.Release()

	// Store the secret first so config.toml never points at a missing value.
	if secretKnob != "" {
		be, err := app.secretBackend()
		if err != nil {
			return finish(opts, err)
		}
		if err := be.Set(ctx, companion.SecretRef(profileName, id, secretKnob), []byte(inline[secretKnob])); err != nil {
			return finish(opts, fmt.Errorf("store %s: %w", secretKnob, err))
		}
	}
	names := sortedKeys(inline)
	if err := app.editConfig(func(e *config.Editor) {
		for _, knob := range names {
			value := inline[knob]
			if knob == secretKnob {
				value = "" // marker: the value lives in the secret backend
			}
			e.SetProfileCompanion(profileName, id, knob, value)
		}
	}); err != nil {
		return finish(opts, err)
	}
	fmt.Printf("Bound companion %s for profile %s: %s\n", id, profileName, strings.Join(names, ", "))
	if app.miseActivated() {
		fmt.Println("Re-run `kae pin` in a bound directory to refresh its fragment.")
	}
	return constants.ExitOK
}

// parseCompanionKnobs validates KEY=VALUE / bare-KEY arguments against the
// companion spec. Non-secret knobs take KEY=VALUE; a token knob takes a lone
// bare KEY whose value is read from stdin (so secrets bypass argv/shell
// history). The two forms cannot be mixed. secretKnob is the token knob name,
// or "" when none. Both forms return their values in the inline map.
func parseCompanionKnobs(spec companion.Spec, args []string, stdin io.Reader) (inline map[string]string, secretKnob string, err error) {
	inline = map[string]string{}
	bare := []string{}
	for _, arg := range args {
		knob, value, hasValue := strings.Cut(arg, "=")
		k, ok := spec.Knob(knob)
		if !ok {
			return nil, "", errf(constants.ExitUsage, "companion %s has no knob %q", spec.ID, knob)
		}
		isSecret := spec.Kind == companion.KindToken
		if hasValue {
			if isSecret {
				return nil, "", errf(constants.ExitUsage,
					"%s is a token; pass it as a bare KEY so the value comes from stdin, not the command line", k.Name)
			}
			inline[knob] = value
		} else {
			if !isSecret {
				return nil, "", errf(constants.ExitUsage, "%s needs a value: %s=VALUE", k.Name, k.Name)
			}
			bare = append(bare, knob)
		}
	}
	switch {
	case len(bare) == 0:
		if len(inline) == 0 {
			return nil, "", errf(constants.ExitUsage, "no knobs given")
		}
	case len(bare) == 1 && len(inline) == 0:
		data, rerr := io.ReadAll(stdin)
		if rerr != nil {
			return nil, "", fmt.Errorf("read value from stdin: %w", rerr)
		}
		value := strings.TrimRight(string(data), "\r\n")
		if value == "" {
			return nil, "", errf(constants.ExitUsage, "no value on stdin for %s", bare[0])
		}
		inline[bare[0]] = value
		secretKnob = bare[0]
	default:
		return nil, "", errf(constants.ExitUsage,
			"pass either KEY=VALUE pairs (non-secret) or a single bare token KEY (value on stdin), not both")
	}
	return inline, secretKnob, nil
}

func runCompanionRm(ctx context.Context, app *App, opts commonOpts, positionals []string) int {
	if len(positionals) < 2 {
		return usageError("usage: %s companion rm <profile> <id> [KEY...]", toolName)
	}
	profileName, id := positionals[0], positionals[1]
	spec, err := app.resolveCompanionTarget(profileName, id)
	if err != nil {
		return finish(opts, err)
	}
	data, bound := app.Config.Profiles[profileName].Companions[id]
	if !bound {
		return finish(opts, errf(constants.ExitNotFound, "profile %q does not bind companion %q", profileName, id))
	}
	knobs := positionals[2:]
	// Determine which knobs to drop, and which of those carry a secret.
	var drop []string
	if len(knobs) == 0 {
		drop = sortedKeys(data)
	} else {
		for _, knob := range knobs {
			if _, ok := data[knob]; !ok {
				return finish(opts, errf(constants.ExitNotFound, "companion %s in profile %q has no knob %q", id, profileName, knob))
			}
		}
		drop = knobs
	}

	cfgLock, err := app.acquireConfigLock()
	if err != nil {
		return finish(opts, err)
	}
	defer cfgLock.Release()

	if spec.Secret() {
		be, err := app.secretBackend()
		if err != nil {
			return finish(opts, err)
		}
		for _, knob := range drop {
			if err := be.Delete(ctx, companion.SecretRef(profileName, id, knob)); err != nil {
				return finish(opts, fmt.Errorf("delete secret %s: %w", knob, err))
			}
		}
	}
	removeWhole := len(knobs) == 0 || len(drop) == len(data)
	if err := app.editConfig(func(e *config.Editor) {
		if removeWhole {
			e.RemoveProfileCompanion(profileName, id)
			return
		}
		for _, knob := range drop {
			e.RemoveProfileCompanionKnob(profileName, id, knob)
		}
	}); err != nil {
		return finish(opts, err)
	}
	if removeWhole {
		fmt.Printf("Removed companion %s from profile %s\n", id, profileName)
	} else {
		fmt.Printf("Removed %d knob(s) from companion %s in profile %s: %s\n", len(drop), id, profileName, strings.Join(drop, ", "))
	}
	return constants.ExitOK
}

type companionKnobItem struct {
	Knob   string `json:"knob"`
	Secret bool   `json:"secret"`
	Value  string `json:"value,omitempty"` // non-secret knobs only; never a token value
}

type companionItem struct {
	Profile   string              `json:"profile"`
	Companion string              `json:"companion"`
	Knobs     []companionKnobItem `json:"knobs"`
}

type companionListReport struct {
	SchemaVersion int             `json:"schema_version"`
	Bindings      []companionItem `json:"bindings"`
}

func runCompanionList(_ context.Context, app *App, opts commonOpts) int {
	if err := app.requireConfig(); err != nil {
		return finish(opts, err)
	}
	report := companionListReport{SchemaVersion: constants.SchemaVersion, Bindings: []companionItem{}}
	for _, profileName := range sortedKeys(app.Config.Profiles) {
		companions := app.Config.Profiles[profileName].Companions
		for _, id := range constants.Companions {
			data, bound := companions[id]
			if !bound {
				continue
			}
			secret := false
			if spec, ok := companion.For(id); ok {
				secret = spec.Secret()
			}
			item := companionItem{Profile: profileName, Companion: id, Knobs: []companionKnobItem{}}
			for _, knob := range sortedKeys(data) {
				ki := companionKnobItem{Knob: knob, Secret: secret}
				if !secret {
					ki.Value = data[knob]
				}
				item.Knobs = append(item.Knobs, ki)
			}
			report.Bindings = append(report.Bindings, item)
		}
	}
	if opts.Format == formatJSON {
		return encodeJSON(report)
	}
	if len(report.Bindings) == 0 {
		fmt.Println("no companion bindings (create one with: kae companion add <profile> <id> KEY=VALUE)")
		return constants.ExitOK
	}
	rows := [][]string{}
	for _, b := range report.Bindings {
		parts := make([]string, len(b.Knobs))
		for i, k := range b.Knobs {
			if k.Secret {
				parts[i] = k.Knob + "=(secret)"
			} else {
				parts[i] = k.Knob + "=" + k.Value
			}
		}
		rows = append(rows, []string{b.Profile, b.Companion, strings.Join(parts, ", ")})
	}
	printTable([]string{"Profile", "Companion", "Knobs"}, rows)
	return constants.ExitOK
}

// CmdCompanionToken is the hidden credential helper the mise exec() template
// invokes to resolve a token knob at environment-evaluation time. It prints the
// raw secret to stdout — the one documented exception to "secrets never reach
// stdout" (docs/SECURITY.md), a git-credential-helper-style seam used only by
// mise, never on a human/JSON reporting path. Usage: kae __companion-token
// <profile> <id> <knob>.
func CmdCompanionToken(ctx context.Context, args []string) int {
	return companionToken(ctx, newApp(""), args)
}

func companionToken(ctx context.Context, app *App, args []string) int {
	if len(args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: kae __companion-token <profile> <id> <knob>")
		return constants.ExitUsage
	}
	be, err := app.secretBackend()
	if err != nil {
		fmt.Fprintln(os.Stderr, "kae:", err)
		return exitOf(err)
	}
	value, found, err := be.Get(ctx, companion.SecretRef(args[0], args[1], args[2]))
	if err != nil {
		fmt.Fprintln(os.Stderr, "kae:", err)
		return exitOf(err)
	}
	if !found {
		fmt.Fprintf(os.Stderr, "kae: companion token %s/%s/%s is not stored (run: kae companion add %s %s %s)\n",
			args[0], args[1], args[2], args[0], args[1], args[2])
		return constants.ExitNotFound
	}
	os.Stdout.Write(value)
	return constants.ExitOK
}

// sortedKeys returns the keys of m in lexical order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
