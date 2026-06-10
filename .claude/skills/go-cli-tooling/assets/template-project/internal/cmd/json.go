package cmd

import (
	"encoding/json"
	"os"
)

func encodeJSON(value any) int {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return fail(err)
	}
	return exitOK
}
