package cmd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/l10n"
)

// The flag package parses kae's command lines, but kae writes what a parse
// failure prints: the error line per kind and the usage block, in the selected
// language (docs/CLI.md § Localization, "The `flag` package"). The English
// rendering is byte for byte what the flag package itself would print, which
// flagusage_test.go checks against a real flag.FlagSet for every command.

// newFlagSet is the one place a flag.FlagSet is made (the catalog test refuses
// flag.NewFlagSet anywhere else). The set prints nothing itself: parseCommon
// renders its failures, and flagSetFor's sets are never parsed.
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {} // the usage block is kae's (printFlagUsage)
	return fs
}

// flagHelp is one flag of a usage block, taken before parseCommon wraps the
// flag's Value: flag.UnquoteUsage and the zero-value test read the Value's type,
// which the wrapper would hide.
type flagHelp struct {
	flag        flag.Flag // a copy holding the original Value
	placeholder string    // the value name UnquoteUsage gives, "" for a bool flag
	defaultArg  string    // the `(default %s)` argument, "" when the default is the zero value
}

// snapshotFlags records the usage block of fs, in the flag package's order.
func snapshotFlags(fs *flag.FlagSet) []flagHelp {
	var out []flagHelp
	fs.VisitAll(func(f *flag.Flag) {
		placeholder, _ := flag.UnquoteUsage(f)
		h := flagHelp{flag: *f, placeholder: placeholder}
		if !flagDefaultIsZero(f) {
			if isStringFlag(f) {
				h.defaultArg = strconv.Quote(f.DefValue)
			} else {
				h.defaultArg = f.DefValue
			}
		}
		out = append(out, h)
	})
	return out
}

// isStringFlag reports whether f holds the flag package's string Value, whose
// default the usage block prints with %q: the one standard Value whose Get
// returns a string.
func isStringFlag(f *flag.Flag) bool {
	g, ok := f.Value.(flag.Getter)
	if !ok {
		return false
	}
	_, isString := g.Get().(string)
	return isString
}

// flagDefaultIsZero is the flag package's isZeroValue: whether DefValue is what a
// zero Value of the flag's type prints. A String method that panics on the zero
// value counts as zero (the flag package lists those after the block; no kae
// Value panics, which the comparison test would show).
func flagDefaultIsZero(f *flag.Flag) (zero bool) {
	typ := reflect.TypeOf(f.Value)
	var z reflect.Value
	if typ.Kind() == reflect.Pointer {
		z = reflect.New(typ.Elem())
	} else {
		z = reflect.Zero(typ)
	}
	defer func() {
		if recover() != nil {
			zero = true
		}
	}()
	return f.DefValue == z.Interface().(flag.Value).String()
}

// setFailure is the one Set that failed during a parse: the flag package stops
// at the first failure.
type setFailure struct {
	name, value string
	boolFlag    bool
	err         error
}

// recordingValue wraps a flag's Value to keep the error its Set returns, which
// the flag package would otherwise flatten into an English string.
type recordingValue struct {
	flag.Value
	name   string
	failed **setFailure
}

func (v *recordingValue) Set(value string) error {
	err := v.Value.Set(value)
	if err != nil && *v.failed == nil {
		*v.failed = &setFailure{name: v.name, value: value, boolFlag: v.IsBoolFlag(), err: err}
	}
	return err
}

// IsBoolFlag passes the wrapped Value's answer through, so the flag package
// still reads a bool flag without an argument.
func (v *recordingValue) IsBoolFlag() bool {
	b, ok := v.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

// recordSetFailures wraps every Value of fs and returns where the first failing
// Set is recorded.
func recordSetFailures(fs *flag.FlagSet) **setFailure {
	failed := new(*setFailure)
	fs.VisitAll(func(f *flag.Flag) {
		f.Value = &recordingValue{Value: f.Value, name: f.Name, failed: failed}
	})
	return failed
}

// parseFailure is the kind of a parse error kae renders.
type parseFailure int

const (
	failureUnexplained   parseFailure = iota // rendered verbatim
	failureUnknown                           // flag provided but not defined
	failureNeedsArgument                     // flag needs an argument
)

// classifyParseFailure reads args again with the flag package's grammar (stop at
// `--` or the first positional, split `name=value`, a valued flag takes the next
// argument) to find which flag failed, without reading the error's text. Set
// failures are known from the wrappers and are not looked for here.
func classifyParseFailure(fs *flag.FlagSet, args []string) (parseFailure, string) {
	for i := 0; i < len(args); i++ {
		name, _, hasValue, ok := splitFlagArg(args[i])
		if !ok {
			return failureUnexplained, "" // `--`, a positional or bad syntax
		}
		f := fs.Lookup(name)
		switch {
		case f == nil:
			return failureUnknown, name
		case !flagTakesValue(f) || hasValue:
		case i+1 < len(args):
			i++ // the next argument is the value
		default:
			return failureNeedsArgument, name
		}
	}
	return failureUnexplained, ""
}

// reportParseError writes what a failed fs.Parse prints: the error line for its
// kind, then the usage block. failed is recordSetFailures' record.
func reportParseError(fs *flag.FlagSet, args []string, err error, failed *setFailure, help []flagHelp) {
	switch {
	case errors.Is(err, flag.ErrHelp):
	case failed != nil && failed.boolFlag:
		stderrf("invalid boolean value %q for %s: %s", failed.value, "-"+failed.name, failed.err)
	case failed != nil:
		stderrf("invalid value %q for flag %s: %s", failed.value, "-"+failed.name, failed.err)
	default:
		switch kind, name := classifyParseFailure(fs, args); kind {
		case failureUnknown:
			stderrf("flag provided but not defined: %s", "-"+name)
		case failureNeedsArgument:
			stderrf("flag needs an argument: %s", "-"+name)
		default:
			fmt.Fprintln(os.Stderr, l10n.Render(err)) // a kind kae does not render
		}
	}
	printFlagUsage(fs.Name(), help)
}

// printFlagUsage writes the usage block on stderr: in English, what the flag
// package's default usage prints (`Usage of <name>:` and PrintDefaults).
func printFlagUsage(name string, help []flagHelp) {
	stderrf("Usage of %s:", name)
	for _, h := range help {
		var b strings.Builder
		b.WriteString("  -" + h.flag.Name)
		if h.placeholder != "" {
			b.WriteString(" " + h.placeholder)
		}
		// A one-letter bool flag keeps its description on the same line, as the
		// flag package does.
		if b.Len() <= 4 {
			b.WriteString("\t")
		} else {
			b.WriteString("\n    \t")
		}
		localized := h.flag
		localized.Usage = l10n.FlagUsage(&h.flag)
		_, usage := flag.UnquoteUsage(&localized)
		b.WriteString(strings.ReplaceAll(usage, "\n", "\n    \t"))
		if h.defaultArg != "" {
			suffix := l10n.Sprintf("(default %s)", h.defaultArg)
			// Japanese full-width parentheses take no space before them
			// (docs/L10N-JA.md § 句読点・括弧・空白).
			if !strings.HasPrefix(suffix, "（") {
				b.WriteString(" ")
			}
			b.WriteString(suffix)
		}
		fmt.Fprint(os.Stderr, b.String(), "\n")
	}
}
