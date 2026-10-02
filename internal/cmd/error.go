package cmd

import (
	"fmt"
	"os"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

func usageError(format string, args ...any) int {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	return constants.ExitUsage
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
