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

// message is a fragment of a kae message carried as a value (l10n.Message): a
// did-you-mean suffix or a remedy that another message embeds as an argument, so
// a human sink renders it in the parent's language. It is not a failure and has
// no exit code. The zero message renders "" in every language.
type message struct {
	format  string
	args    []any
	english string
}

func (m message) Error() string { return m.english }

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
