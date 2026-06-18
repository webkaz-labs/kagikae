package cmd

import (
	"flag"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

// Extra-flag registrars. Each registers a command's non-common flags on fs.
// Commands call these from their parseCommon extra closure; flagSetFor calls
// them with throwaway targets to enumerate a command's flags for
// `kae __complete flags <cmd>`. Defining each flag name exactly once here keeps
// the completion list from drifting from what the parser actually accepts.

func registerAddFlags(fs *flag.FlagSet, restore, noLogin *bool) {
	fs.BoolVar(restore, "restore", false, "restore the previous login after capturing (login flow only)")
	fs.BoolVar(noLogin, "no-login", false, "snapshot the current live auth state without launching a login flow")
}

func registerUseFlags(fs *flag.FlagSet, shared, isolated, quiet *bool, profile *string) {
	registerScopeFlags(fs, shared, isolated)
	fs.BoolVar(quiet, "quiet", false, "suppress the success report (for hooks; bare use)")
	registerProfileFlag(fs, profile)
}

func registerPinFlags(fs *flag.FlagSet, shared, isolated *bool) {
	registerScopeFlags(fs, shared, isolated)
}

func registerRunFlags(fs *flag.FlagSet, shared, isolated, envMode *bool, profile *string) {
	registerScopeFlags(fs, shared, isolated)
	fs.BoolVar(envMode, "env", false, "inject the env-profile vars only (no home redirect, no lock)")
	registerProfileFlag(fs, profile)
}

func registerMiseInitFlags(fs *flag.FlagSet, profile, mode *string, auto, write *bool) {
	registerProfileFlag(fs, profile)
	// --mode is still parsed so an old `--mode bond|pin|home|overlay` invocation
	// gets a clear rejection rather than "flag not defined".
	fs.StringVar(mode, "mode", constants.ModeAuth, "rendered integration (auth only; bind directories with kae pin)")
	fs.BoolVar(auto, "auto", false, "add a [hooks.enter] running `kae use --quiet`")
	fs.BoolVar(write, "write", false, "write/update .mise.toml in the current directory")
}

func registerAccountRmFlags(fs *flag.FlagSet, force *bool) {
	fs.BoolVar(force, "force", false, "remove even the active account, dropping it from state")
}

func registerProfileRmFlags(fs *flag.FlagSet, force *bool) {
	fs.BoolVar(force, "force", false, "remove even the default profile, clearing default_profile")
}

func registerProfileDefaultFlags(fs *flag.FlagSet, clear *bool) {
	fs.BoolVar(clear, "clear", false, "clear default_profile")
}

func registerRollbackFlags(fs *flag.FlagSet, to *string) {
	fs.StringVar(to, "to", "", "backup id to restore (default: most recent)")
}

func registerCompletionFlags(fs *flag.FlagSet, install *bool) {
	fs.BoolVar(install, "install", false, "register the completion script interactively")
}

// commandFlagSpec describes how a command builds its flag set, so flagSetFor can
// reproduce it for `kae __complete flags`.
type commandFlagSpec struct {
	dryRun bool                // whether parseCommon was called with withDryRun
	extra  func(*flag.FlagSet) // the command's extra-flag registrar (throwaway targets)
}

// commandFlagSpecs maps each public command (and its router aliases) to its flag
// spec. A command with only the common flags is absent (the zero spec yields the
// common set). Subcommand-only flags are attached to the parent command so
// `kae account --<TAB>` / `kae profile --<TAB>` still offer them.
var commandFlagSpecs = map[string]commandFlagSpec{
	"add": {dryRun: true, extra: func(fs *flag.FlagSet) { registerAddFlags(fs, new(bool), new(bool)) }},
	"use": {dryRun: true, extra: func(fs *flag.FlagSet) { registerUseFlags(fs, new(bool), new(bool), new(bool), new(string)) }},
	"u":   {dryRun: true, extra: func(fs *flag.FlagSet) { registerUseFlags(fs, new(bool), new(bool), new(bool), new(string)) }},
	"pin": {extra: func(fs *flag.FlagSet) { registerPinFlags(fs, new(bool), new(bool)) }},
	"p":   {extra: func(fs *flag.FlagSet) { registerPinFlags(fs, new(bool), new(bool)) }},
	"run": {extra: func(fs *flag.FlagSet) { registerRunFlags(fs, new(bool), new(bool), new(bool), new(string)) }},
	"r":   {extra: func(fs *flag.FlagSet) { registerRunFlags(fs, new(bool), new(bool), new(bool), new(string)) }},
	"mise": {extra: func(fs *flag.FlagSet) {
		registerMiseInitFlags(fs, new(string), new(string), new(bool), new(bool))
	}},
	"completion": {extra: func(fs *flag.FlagSet) { registerCompletionFlags(fs, new(bool)) }},
	"rollback":   {dryRun: true, extra: func(fs *flag.FlagSet) { registerRollbackFlags(fs, new(string)) }},
	"account": {dryRun: true, extra: func(fs *flag.FlagSet) {
		registerAccountRmFlags(fs, new(bool)) // account rm --force
	}},
	"profile": {dryRun: true, extra: func(fs *flag.FlagSet) {
		registerProfileRmFlags(fs, new(bool))      // profile rm --force
		registerProfileDefaultFlags(fs, new(bool)) // profile default --clear
	}},
}

// flagSetFor builds the flag set a command parses (common flags + the command's
// extras), so `kae __complete flags <cmd>` can list exactly the flags the parser
// accepts. An unknown command yields the common flags only.
func flagSetFor(cmd string) *flag.FlagSet {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	var opts commonOpts
	spec := commandFlagSpecs[cmd]
	registerCommonFlags(fs, &opts, spec.dryRun)
	if spec.extra != nil {
		spec.extra(fs)
	}
	return fs
}

// flagCompletions returns the command's flags as completion tokens (`--name`, or
// `-n` for single-character flags), in flag.FlagSet's lexical order.
func flagCompletions(cmd string) []string {
	var out []string
	flagSetFor(cmd).VisitAll(func(f *flag.Flag) {
		if len(f.Name) == 1 {
			out = append(out, "-"+f.Name)
		} else {
			out = append(out, "--"+f.Name)
		}
	})
	return out
}
