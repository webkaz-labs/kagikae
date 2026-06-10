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
