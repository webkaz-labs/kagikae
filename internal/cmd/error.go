package cmd

import (
	"fmt"
	"os"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/l10n"
)

// usageError prints a usage error, localized, and returns the usage exit code.
// It forwards its unchanged format and args to l10n.Sprintf, which keeps it a
// `go vet` printf wrapper; the newline is added by Fprintln, not to the format.
func usageError(format string, args ...any) int {
	fmt.Fprintln(os.Stderr, l10n.Sprintf(format, args...))
	return constants.ExitUsage
}

// warnf writes a `kae: warning:` line, localized. The prefix stays English
// (docs/CLI.md § Localization) and the catalog key is the text after it, without
// the newline. Like usageError it forwards its unchanged format and args to
// l10n.Sprintf, which keeps it a `go vet` printf wrapper; Fprintln adds the
// newline.
func warnf(format string, args ...any) {
	fmt.Fprintln(os.Stderr, "kae: warning: "+l10n.Sprintf(format, args...))
}

// notef is warnf for a `kae: note:` line.
func notef(format string, args ...any) {
	fmt.Fprintln(os.Stderr, "kae: note: "+l10n.Sprintf(format, args...))
}

// infof is warnf for the bare `kae:` line that continues a warning (the cause or
// the remedy on a second line).
func infof(format string, args ...any) {
	fmt.Fprintln(os.Stderr, "kae: "+l10n.Sprintf(format, args...))
}

// reportf writes a line of a report on stdout, localized: the stdout counterpart
// of warnf. Like usageError it forwards its unchanged format and args to
// l10n.Sprintf, which keeps it a `go vet` printf wrapper; Fprintln adds the
// newline, so a format ends without one. os.Stdout is read at call time so a test
// that swaps it sees the line.
func reportf(format string, args ...any) {
	fmt.Fprintln(os.Stdout, l10n.Sprintf(format, args...))
}

// reportMessage is reportf for text that already travels as a value (a message
// or an error that renders verbatim), which has no format for the catalog test
// to judge.
func reportMessage(m error) {
	fmt.Fprintln(os.Stdout, l10n.Render(m))
}

// promptf writes an interactive prompt on stderr, localized, without a newline so
// the answer is typed on the same line. The answers a prompt accepts do not
// depend on the language (docs/CLI.md § Localization). It forwards its unchanged
// format and args to l10n.Sprintf, which keeps it a `go vet` printf wrapper.
func promptf(format string, args ...any) {
	fmt.Fprint(os.Stderr, l10n.Sprintf(format, args...))
}

// warnMessage writes a `kae: warning:` line whose text already travels as a value
// (a message, or an error that renders verbatim): it has no format, so the catalog
// test has no sink to judge.
func warnMessage(m error) {
	fmt.Fprintln(os.Stderr, "kae: warning: "+l10n.Render(m))
}

// message is kae text carried as a value (l10n.Msg). It is an alias so the call
// sites in this package keep their spelling.
type message = l10n.Msg

// msgf builds a message. It forwards its unchanged format and args to l10n.Msgf,
// which keeps it a `go vet` printf wrapper.
func msgf(format string, args ...any) message {
	return l10n.Msgf(format, args...)
}

// joinMessages joins messages with "; ", on the values, so each one still renders in
// the selected language. It has at least one element.
func joinMessages(ms []message) message {
	joined := ms[0]
	for _, m := range ms[1:] {
		joined = msgf("%s; %s", joined, m)
	}
	return joined
}

// unsupportedShellFormat is the usage error for a shell kae has no completion for.
const unsupportedShellFormat = "unsupported shell %q (supported: bash, zsh, fish)"

// errLaunchLogin wraps the failure to start a tool's own login flow.
func errLaunchLogin(tool string, err error) error {
	return fmt.Errorf("launch %s login: %w", tool, err)
}

// errResolveCwd wraps the failure to read the working directory.
func errResolveCwd(err error) error {
	return fmt.Errorf("resolve the current directory: %w", err)
}
