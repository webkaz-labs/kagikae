package l10n

import (
	"fmt"
	"strings"
)

// Msg is the concrete value of a Message: kae text carried as data before it is
// shown, as its English format and arguments. A whole warning, a refusal reason,
// a check message, or a fragment another message embeds as an argument is a Msg,
// so a human sink renders it in the selected language (Render) while Error() and
// JSON stay English. It is not a failure and has no exit code. The zero Msg
// renders "" in every language.
//
// A Msg holds a slice, so it cannot be compared with == or used as a map key. It is a
// struct, so `omitempty` never omits it: a string field that carried `omitempty` keeps
// its JSON bytes only if the Msg field drops the tag and the writer skips the empty case.
type Msg struct {
	kind    msgKind
	format  string
	args    []any
	english string
}

// msgKind says how Render treats a Msg, so no format string doubles as a marker.
type msgKind int

const (
	kindPlain     msgKind = iota // a catalog format and its arguments
	kindOf                       // only carries an error (Of)
	kindUnstopped                // only drops its argument's closing full stop (Unstopped)
	kindList                     // only joins verbatim items (List)
)

// Msgf builds a Msg. It hands its unchanged format and args to fmt.Sprintf,
// which keeps it a `go vet` printf wrapper; the catalog test judges its callers'
// format as a sink.
func Msgf(format string, args ...any) Msg {
	return Msg{format: format, args: args, english: fmt.Sprintf(format, args...)}
}

// Of carries an error as a Msg: the field type of a check message or a warning
// that is sometimes a kae message and sometimes the text of an external cause (an
// OS, upstream or standard-library error), which a human sink quotes verbatim.
// An error that is already a Msg is returned unchanged; another Message renders
// localized through Render and any other error renders its own Error(). Error()
// and JSON are the English text in every case. A nil error is the zero Msg.
func Of(err error) Msg {
	if err == nil {
		return Msg{}
	}
	if m, ok := err.(Msg); ok {
		return m
	}
	return Msg{kind: kindOf, format: "%v", args: []any{err}, english: err.Error()}
}

// Unstopped carries m so that a human sink renders it without a closing Japanese
// full stop ("。"), for a message that is a sentence when shown alone but is joined
// to others or wrapped in parentheses, where its own stop would double up ("。。",
// "。）"). Error() and JSON stay m's English text: English messages carry no stop.
func Unstopped(m Msg) Msg {
	if m.Empty() {
		return m
	}
	return Msg{kind: kindUnstopped, format: "%s", args: []any{m}, english: m.english}
}

// listSeparators join a List's items: ", " in English, "、" in Japanese
// (docs/L10N-JA.md § 句読点・括弧・空白).
var listSeparators = map[Language]string{English: ", ", Japanese: "、"}

// List carries names or values enumerated in a message (tools, paths, targets),
// each inserted verbatim: a human sink joins them with the selected language's
// separator, while Error() and JSON join them with ", ". No items is the zero Msg.
func List(items []string) Msg {
	if len(items) == 0 {
		return Msg{}
	}
	items = append([]string(nil), items...)
	return Msg{kind: kindList, format: "%s", args: []any{items}, english: strings.Join(items, listSeparators[English])}
}

// Error renders the English text, which is what generated files and errors.Is/As
// see.
func (m Msg) Error() string { return m.english }

// Empty reports whether m is the zero Msg (no text in any language).
func (m Msg) Empty() bool { return m.format == "" }

// MessageFormat makes Msg a Message.
func (m Msg) MessageFormat() (string, []any) { return m.format, m.args }

// MarshalText renders the English text for JSON. It is MarshalText and not
// MarshalJSON on purpose: json.Marshal would escape `<`, `>` and `&` in the
// result of a MarshalJSON, which breaks the byte-for-byte equality with the
// plain string field the Msg replaces under an encoder that has
// SetEscapeHTML(false). There is deliberately no UnmarshalText: a Msg is built
// by kae, never read back, and a test that decodes a report uses a wire struct.
func (m Msg) MarshalText() ([]byte, error) { return []byte(m.english), nil }
