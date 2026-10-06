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

func registerAddFlags(fs *flag.FlagSet, restore, noLogin *bool, identity *string) {
	fs.BoolVar(restore, "restore", false, "restore the previous login after capturing (login flow only)")
	fs.BoolVar(noLogin, "no-login", false, "snapshot the current live auth state without launching a login flow")
	fs.StringVar(identity, "identity", "", "record this login identity for the account when auto-detection is unavailable (e.g. agy on current Antigravity)")
}

func registerUseFlags(fs *flag.FlagSet, shared, isolated, quiet, auto, noRestart *bool, profile *string) {
	registerScopeFlags(fs, shared, isolated)
	fs.BoolVar(auto, "auto", false, "apply a resolved profile while preserving global isolated selections")
	fs.BoolVar(quiet, "quiet", false, "suppress the success report (for hooks; bare use)")
	registerNoRestartFlag(fs, noRestart)
	registerProfileFlag(fs, profile)
}

// registerNoRestartFlag is --no-restart, for every command that reconciles
// resident processes after a switch (docs/CLI.md § Global Flags).
func registerNoRestartFlag(fs *flag.FlagSet, noRestart *bool) {
	fs.BoolVar(noRestart, "no-restart", false, "do not restart codex's managed daemon after the switch; warn instead")
}

// registerFullFlag is --full and its -f short form, which status, accounts and
// ls take for their account tables (printAccountTable).
func registerFullFlag(fs *flag.FlagSet, full *bool) {
	fs.BoolVar(full, "full", false, "show every column of the account tables, Identity and Driver included")
	fs.BoolVar(full, "f", false, "alias for --full")
}

// parseFullCommand is parseCommon for a command whose one extra flag is
// --full (status and accounts).
func parseFullCommand(name string, flags []string) (commonOpts, bool) {
	var full bool
	opts, ok := parseCommon(name, flags, false, func(fs *flag.FlagSet) { registerFullFlag(fs, &full) })
	opts.Full = full
	return opts, ok
}

func registerLsFlags(fs *flag.FlagSet, f *lsFlags) {
	fs.BoolVar(&f.pins, "pins", false, "list every directory bound with kae pin (alias of kae ls pin)")
	registerFullFlag(fs, &f.full)
	fs.BoolVar(&f.current, "current", false, "print the current place's path for the target")
	registerPlaceFlags(fs, f)
}

// registerPlaceFlags is the place selection `kae ls`, `kae open` and `kae cd`
// share. open and cd register it alone: they always choose one place, so they
// take no --current (their default) and no --pins.
func registerPlaceFlags(fs *flag.FlagSet, f *lsFlags) {
	fs.Var(&f.at, "at", "the place number N of the target's kae ls listing")
	fs.BoolVar(&f.project, "project", false, "the nearest effective ancestor project level")
	fs.BoolVar(&f.below, "below", false, "a project level below the current directory")
	fs.BoolVar(&f.home, "home", false, "the tool's real home")
	fs.BoolVar(&f.root, "root", false, "the directory holding a project level's .claude/ or .codex/")
	registerScopeFlags(fs, &f.shared, &f.isolated)
}

// registerNavigateFlags is every flag `kae open` and `kae cd` take beyond the
// common ones.
func registerNavigateFlags(fs *flag.FlagSet, f *lsFlags) {
	registerPlaceFlags(fs, f)
	registerPickFlag(fs, &f.pick)
}

// registerPickFlag is the picker `kae open` and `kae cd` add to the place
// selection; `kae ls` prints, so it has nothing to pick.
func registerPickFlag(fs *flag.FlagSet, pick *bool) {
	fs.BoolVar(pick, "pick", false, "choose among the target's places in the picker, even when it has a current place")
}

// registerPinFlags carries -t/--tree on top of the shared scope pair: the tree mode is
// kae pin's alone, so use and run, which register the pair without it, reject -t as an
// undefined flag (exit 64).
func registerPinFlags(fs *flag.FlagSet, shared, isolated, tree, noLink *bool) {
	registerScopeFlags(fs, shared, isolated)
	fs.BoolVar(tree, "tree", false, "one private store for this directory's tree, kept across account switches (claude only)")
	fs.BoolVar(tree, "t", false, "alias for --tree")
	fs.BoolVar(noLink, "no-link", false,
		"do not leave ./.config/<tool> links to this directory's stores (and remove the ones kae made here)")
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
	fs.BoolVar(auto, "auto", false, "add a [hooks.enter] running kae use --auto --quiet")
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
	fs.StringVar(to, "to", "", "backup id to restore (default: most recent restorable)")
}

// registerUnpinFlags is the `kae unpin` extra-flag registrar, shared with the
// completion flag registry so `kae unpin --<TAB>` offers it.
func registerUnpinFlags(fs *flag.FlagSet, purge *bool) {
	fs.BoolVar(purge, "purge", false, "also delete this directory's per-directory keychain credentials (sessions and settings are kept)")
}

func registerCompletionFlags(fs *flag.FlagSet, install, refresh, noFunction *bool) {
	fs.BoolVar(install, "install", false, "register the completion script interactively")
	fs.BoolVar(noFunction, "no-function", false, "print the completion alone, without the kae shell function (the shape of a completion file)")
	fs.BoolVar(refresh, "refresh", false, "rewrite already-registered completion files from this binary (no shell arg; never creates a new registration)")
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
	"uninstall": {dryRun: true, extra: func(fs *flag.FlagSet) { registerUninstallFlags(fs, new([]string)) }},
	"add":       {dryRun: true, extra: func(fs *flag.FlagSet) { registerAddFlags(fs, new(bool), new(bool), new(string)) }},
	"use": {dryRun: true, extra: func(fs *flag.FlagSet) {
		registerUseFlags(fs, new(bool), new(bool), new(bool), new(bool), new(bool), new(string))
	}},
	"status":   {extra: func(fs *flag.FlagSet) { registerFullFlag(fs, new(bool)) }},
	"accounts": {extra: func(fs *flag.FlagSet) { registerFullFlag(fs, new(bool)) }},
	"ls":       {extra: func(fs *flag.FlagSet) { registerLsFlags(fs, new(lsFlags)) }},
	"open":     {extra: func(fs *flag.FlagSet) { registerNavigateFlags(fs, new(lsFlags)) }},
	"cd":       {extra: func(fs *flag.FlagSet) { registerNavigateFlags(fs, new(lsFlags)) }},
	"pin":      {extra: func(fs *flag.FlagSet) { registerPinFlags(fs, new(bool), new(bool), new(bool), new(bool)) }},
	"unpin":    {extra: func(fs *flag.FlagSet) { registerUnpinFlags(fs, new(bool)) }},
	"run":      {extra: func(fs *flag.FlagSet) { registerRunFlags(fs, new(bool), new(bool), new(bool), new(string)) }},
	"mise": {extra: func(fs *flag.FlagSet) {
		registerMiseInitFlags(fs, new(string), new(string), new(bool), new(bool))
	}},
	"completion":   {extra: func(fs *flag.FlagSet) { registerCompletionFlags(fs, new(bool), new(bool), new(bool)) }},
	"preservation": {dryRun: true},
	"rollback":     {dryRun: true, extra: func(fs *flag.FlagSet) { registerRollbackFlags(fs, new(string)) }},
	"account": {dryRun: true, extra: func(fs *flag.FlagSet) {
		registerAccountRmFlags(fs, new(bool)) // account rm --force
	}},
	"profile": {dryRun: true, extra: func(fs *flag.FlagSet) {
		registerProfileRmFlags(fs, new(bool))      // profile rm --force
		registerProfileDefaultFlags(fs, new(bool)) // profile default --clear
	}},
}

// commandAliases maps the router's command aliases to their canonical name so
// flagSetFor keys commandFlagSpecs by canonical name only (the completion script
// passes the command word verbatim, alias or not).
var commandAliases = map[string]string{"u": "use", "p": "pin", "r": "run", "d": "doctor", "s": "status"}

// flagSetFor builds the flag set a command parses (common flags + the command's
// extras), so `kae __complete flags <cmd>` can list exactly the flags the parser
// accepts. An unknown command yields the common flags only.
func flagSetFor(cmd string) *flag.FlagSet {
	if canon, ok := commandAliases[cmd]; ok {
		cmd = canon
	}
	fs := newFlagSet(cmd)
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

// valuedFlagCompletions lists both dash spellings accepted by flag.FlagSet.
// Shells use these tokens only to consume values, not as display candidates.
func valuedFlagCompletions(cmd string) []string {
	var out []string
	flagSetFor(cmd).VisitAll(func(f *flag.Flag) {
		if flagTakesValue(f) {
			out = append(out, "-"+f.Name, "--"+f.Name)
		}
	})
	return out
}

// flagTakesValue reports whether the flag package reads the argument after f as
// its value: every flag but a boolean one.
func flagTakesValue(f *flag.Flag) bool {
	value, ok := f.Value.(interface{ IsBoolFlag() bool })
	return !ok || !value.IsBoolFlag()
}
