package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/webkaz-labs/kagikae/internal/constants"
)

func encodeJSON(value any) int {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		fmt.Fprintln(os.Stderr, "kae:", err)
		return constants.ExitError
	}
	return constants.ExitOK
}
