package l10n

import "fmt"

// Error is a kae error as a message value: its English format and arguments, the
// English text fmt.Errorf builds from them, and every cause the format wraps with
// %w. Error() renders that English text, which is what JSON, generated files and
// errors.Is/As see; a human sink renders it localized through Render, and so does
// a message that wraps it as an argument.
//
// Errorf returns a pointer, so an Error is comparable: a sentinel is
// `var ErrX = l10n.Errorf("...")` and errors.Is matches it by identity, whatever
// language renders it. A Msg cannot be a sentinel (it holds a slice, so it is not
// comparable and errors.Is never matches it).
type Error struct {
	format string
	args   []any
	// english is fmt.Errorf(format, args...): the English text and, through it,
	// every cause the format wraps with %w.
	english error
}

// Errorf builds an Error. It hands its unchanged format and args to fmt.Errorf,
// which keeps it a `go vet` printf wrapper that accepts %w; the catalog test
// judges its callers' format as a sink.
func Errorf(format string, args ...any) *Error {
	return &Error{format: format, args: args, english: fmt.Errorf(format, args...)}
}

// Error renders the English text.
func (e *Error) Error() string { return e.english.Error() }

// Unwrap returns every cause the format wrapped with %w, so errors.Is and
// errors.As see through the message whatever language renders it. A format with
// several %w wraps them all, as fmt.Errorf does.
func (e *Error) Unwrap() []error {
	switch wrapped := e.english.(type) {
	case interface{ Unwrap() []error }:
		return wrapped.Unwrap()
	case interface{ Unwrap() error }:
		return []error{wrapped.Unwrap()}
	}
	return nil
}

// MessageFormat makes Error a Message.
func (e *Error) MessageFormat() (string, []any) { return e.format, e.args }
