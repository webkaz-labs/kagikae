package patch

import (
	"errors"

	"github.com/BurntSushi/toml"

	"github.com/webkaz-labs/kagikae/internal/l10n"
)

var errTOMLInvalid = l10n.Errorf("the document has an invalid value or syntax")

// RedactTOMLError is RedactParseError for a TOML document. BurntSushi/toml's and
// tomledit's messages quote the offending input and the last key, and a TOML
// document kae reads can hold secrets (codex's config.toml carries MCP server
// env and headers), so only the line of a syntax error survives. Any other
// decode error, a value of the wrong type or another parser's error, gets one
// fixed reason.
func RedactTOMLError(err error) error {
	var parseErr toml.ParseError
	if errors.As(err, &parseErr) && parseErr.Position.Line > 0 {
		return l10n.Errorf("the document has a syntax error at line %d", parseErr.Position.Line)
	}
	return errTOMLInvalid
}
