package patch

import (
	"bytes"
	"errors"
	"regexp"
	"strconv"
	"unicode/utf8"

	"github.com/BurntSushi/toml"

	"github.com/webkaz-labs/kagikae/internal/l10n"
)

var errTOMLInvalid = l10n.Errorf("the document has an invalid value or syntax")

// RedactTOMLError is RedactParseError for a TOML document decoded with
// BurntSushi/toml. Its messages quote the offending input and the last key, and
// a TOML document kae reads can hold secrets (codex's config.toml carries MCP
// server env and headers), so only the line of a syntax error survives. Any
// other decode error, a value of the wrong type or another parser's error, gets
// one fixed reason.
func RedactTOMLError(err error) error {
	var parseErr toml.ParseError
	if errors.As(err, &parseErr) && parseErr.Position.Line > 0 {
		return l10n.Errorf("the document has a syntax error at line %d", parseErr.Position.Line)
	}
	return errTOMLInvalid
}

// tomledit has no error type: its parser's messages start "at <line>:<byte
// column>: " (1-based line, 0-based byte column of the token) and its
// scanner's "offset <byte offset>: ", and both then quote the input. Only
// these leading numbers are read.
var (
	tomleditLineCol = regexp.MustCompile(`^at ([0-9]+):([0-9]+): `)
	tomleditOffset  = regexp.MustCompile(`^offset ([0-9]+): `)
)

// RedactTOMLEditError is RedactTOMLError for a parse of data with
// github.com/creachadair/tomledit. It keeps the 1-based line and character
// column of the syntax error, resolved against data, and drops the message.
// An error in neither tomledit shape, or a position data does not contain,
// gets RedactTOMLError's fixed reason.
func RedactTOMLEditError(err error, data []byte) error {
	off, ok := tomleditErrorOffset(err.Error(), data)
	if !ok {
		return errTOMLInvalid
	}
	lineStart := bytes.LastIndexByte(data[:off], '\n') + 1
	line := bytes.Count(data[:off], []byte{'\n'}) + 1
	column := utf8.RuneCount(data[lineStart:off]) + 1
	return l10n.Errorf("the document has a syntax error at line %d, column %d", line, column)
}

// tomleditErrorOffset returns the byte offset in data a tomledit error message
// points at.
func tomleditErrorOffset(msg string, data []byte) (int, bool) {
	if m := tomleditLineCol.FindStringSubmatch(msg); m != nil {
		// The scanner does not count the line breaks inside a multi-line
		// literal string ('''), so after one the parser's line and column
		// no longer match data.
		if bytes.Contains(data, []byte("'''")) {
			return 0, false
		}
		line, lerr := strconv.Atoi(m[1])
		col, cerr := strconv.Atoi(m[2])
		if lerr != nil || cerr != nil || line < 1 {
			return 0, false
		}
		lineStart := 0
		for range line - 1 {
			next := bytes.IndexByte(data[lineStart:], '\n')
			if next < 0 {
				return 0, false
			}
			lineStart += next + 1
		}
		lineEnd := len(data)
		if next := bytes.IndexByte(data[lineStart:], '\n'); next >= 0 {
			lineEnd = lineStart + next
		}
		// A line-break token reports the column one past the break, which
		// names the end of its line; any column further out is not in data.
		if col > lineEnd-lineStart+1 {
			return 0, false
		}
		off := lineStart + min(col, lineEnd-lineStart)
		// At the end of a CRLF line, name the line end, not the CR.
		if off == lineEnd && off > lineStart && data[off-1] == '\r' {
			off--
		}
		return off, true
	}
	if m := tomleditOffset.FindStringSubmatch(msg); m != nil {
		end, err := strconv.Atoi(m[1])
		if err != nil || end < 1 || end > len(data) {
			return 0, false
		}
		// The offset is where the scanner stopped: the end of the token it
		// rejected, or the end of input when the input ends inside a token.
		// Either way, name the last rune before it.
		_, size := utf8.DecodeLastRune(data[:end])
		return end - size, true
	}
	return 0, false
}
