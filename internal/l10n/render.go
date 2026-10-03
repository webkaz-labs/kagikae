package l10n

import (
	"fmt"
	"reflect"
)

// Message is a kae message carried as a value before it is shown: its English
// format and arguments. Error() renders the English text, which is what JSON,
// generated files and errors.Is/As see; only a human sink renders the localized
// text, through Render.
type Message interface {
	error
	MessageFormat() (format string, args []any)
}

// Sprintf renders a message for a person, in the process's language. The English
// path hands its unchanged format and args to fmt.Sprintf, which keeps Sprintf
// and every sink forwarding to it a `go vet` printf wrapper; the catalog test, not
// vet, covers the Japanese path.
func Sprintf(format string, args ...any) string {
	if ja, ok := japanese(format); ok {
		return renderJapanese(ja, args)
	}
	return fmt.Sprintf(format, args...)
}

// Render renders err for a person. A Message renders in the process's language;
// any other error is an external or not yet migrated one and renders verbatim.
// A nil error, or a nil pointer in an error, renders `<nil>` as fmt's %v does.
func Render(err error) string {
	if err == nil {
		return "<nil>"
	}
	if v := reflect.ValueOf(err); v.Kind() == reflect.Pointer && v.IsNil() {
		return "<nil>"
	}
	m, ok := err.(Message)
	if !ok {
		return err.Error()
	}
	format, args := m.MessageFormat()
	if ja, ok := japanese(format); ok {
		return renderJapanese(ja, args)
	}
	return err.Error()
}

// japanese returns the Japanese format when Japanese is selected and the catalog
// has the format. A miss falls back to English; the catalog test reports it.
func japanese(format string) (string, bool) {
	if Current() != Japanese {
		return "", false
	}
	return lookup(format)
}

// renderJapanese formats through fmt.Errorf so a `%w` in a message value's
// format renders as `%v` would, as it does in the English Error(). An argument
// that is itself a Message renders localized; any other argument, an external
// error included, is inserted verbatim.
func renderJapanese(format string, args []any) string {
	localized := make([]any, len(args))
	for i, arg := range args {
		if m, ok := arg.(Message); ok {
			localized[i] = localizedError{text: Render(m), cause: m}
			continue
		}
		localized[i] = arg
	}
	return fmt.Errorf(format, localized...).Error()
}

// localizedError stands in for a Message argument while its parent renders, so
// `%w`, `%v` and `%s` all print the localized text.
type localizedError struct {
	text  string
	cause error
}

func (e localizedError) Error() string { return e.text }
func (e localizedError) Unwrap() error { return e.cause }
