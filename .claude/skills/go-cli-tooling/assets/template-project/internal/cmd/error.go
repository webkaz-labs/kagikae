package cmd

import (
	"fmt"
	"os"
)

func fail(err error) int {
	if err == nil {
		return exitOK
	}
	fmt.Fprintln(os.Stderr, err)
	return exitError
}

func usageError(format string, args ...any) int {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	return exitUsage
}
