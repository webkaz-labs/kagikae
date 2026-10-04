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

// warnMessage writes a `kae: warning:` line whose text already travels as a value
// (a message, or an error that renders verbatim): it has no format, so the catalog
// test has no sink to judge.
func warnMessage(m error) {
	fmt.Fprintln(os.Stderr, "kae: warning: "+l10n.Render(m))
}

// warnText writes a `kae: warning:` line from a string kae composed and that
// also reaches JSON or a `kae doctor` check (an adapter's Detect warning, a config
// warning), so it stays English until those reports convert (docs/ROADMAP.md,
// localization stage 3).
func warnText(text string) {
	fmt.Fprintln(os.Stderr, "kae: warning: "+text)
}

// message is kae text carried as a value (l10n.Message): a whole warning, a refusal
// reason, or a fragment (a did-you-mean suffix, a remedy) that another message embeds
// as an argument, so a human sink renders it in the selected language. It is not a failure and has
// no exit code. The zero message renders "" in every language.
type message struct {
	format  string
	args    []any
	english string
}

func (m message) Error() string { return m.english }

// empty reports whether m is the zero message (no text in any language).
func (m message) empty() bool { return m.format == "" }

// MessageFormat makes message an l10n.Message.
func (m message) MessageFormat() (string, []any) { return m.format, m.args }

// msgf builds a message. It hands its unchanged format and args to fmt.Sprintf,
// which keeps it a `go vet` printf wrapper.
func msgf(format string, args ...any) message {
	return message{format: format, args: args, english: fmt.Sprintf(format, args...)}
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
