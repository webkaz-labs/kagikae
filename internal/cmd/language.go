package cmd

import (
	"flag"
	"os"
	"strconv"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/l10n"
)

// selectLanguage decides the process's language from the command line and the
// environment, before any flag is parsed, so a usage error raised while parsing
// a JSON-mode command line is English too (docs/CLI.md § Localization).
func selectLanguage(args []string) {
	l10n.Select(jsonModeArgs(args), os.Getenv)
}

// jsonModeArgs reports whether a command line is in JSON mode: among kae's own
// arguments before any `--`, a `--json`/`-json` with no value or a true one, or a
// `--format`/`-format` given `json`. The value of another flag does not count, so
// the scan consumes the value of every flag the command registers as valued.
func jsonModeArgs(args []string) bool {
	command := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = args[0]
	}
	valued := valuedFlagNames(flagSetFor(command))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return false
		}
		name, value, hasValue, ok := splitFlagArg(arg)
		if !ok {
			continue
		}
		switch {
		case name == "json":
			if !hasValue {
				return true
			}
			if on, err := strconv.ParseBool(value); err == nil && on {
				return true
			}
		case name == "format":
			if !hasValue && i+1 < len(args) {
				i++
				value, hasValue = args[i], true
			}
			if hasValue && value == formatJSON {
				return true
			}
		case valued[name] && !hasValue:
			i++ // the next argument is this flag's value, whatever it looks like
		}
	}
	return false
}

// splitFlagArg splits `-name`, `--name` or either with `=value`, as the flag
// package reads them. ok is false for a positional, a bare `-` and `---name`.
func splitFlagArg(arg string) (name, value string, hasValue, ok bool) {
	body, found := strings.CutPrefix(arg, "--")
	if !found {
		body, found = strings.CutPrefix(arg, "-")
	}
	if !found || body == "" || strings.HasPrefix(body, "-") {
		return "", "", false, false
	}
	name, value, hasValue = strings.Cut(body, "=")
	return name, value, hasValue, name != ""
}

// valuedFlagNames lists the flags of fs that consume a value.
func valuedFlagNames(fs *flag.FlagSet) map[string]bool {
	valued := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) {
		if flagTakesValue(f) {
			valued[f.Name] = true
		}
	})
	return valued
}
