package patch

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/creachadair/tomledit"
)

// tomledit's own messages quote the document; the redacted error keeps only the
// line and character column, resolved against the document. The cases run the
// real parser so a tomledit upgrade that changes its message shape fails here
// instead of silently falling back to the fixed reason.
func TestRedactTOMLEditErrorKeepsOnlyThePosition(t *testing.T) {
	const placeholder = "sk-placeholder"
	cases := []struct {
		name, doc, want string
	}{
		// Parser errors ("at <line>:<byte column>: ...").
		{"unclosed table header", "[profiles\n", "line 1, column 10"},
		{"unclosed table header on a CRLF line", "[profiles\r\n", "line 1, column 10"},
		{"unclosed array table header", "token = \"" + placeholder + "\"\n[[x]\n", "line 2, column 5"},
		{"word after a value", "name = \"é\" " + placeholder + "\n", "line 1, column 12"},
		{"unclosed array at end of file", "a = [\"" + placeholder + "\",\n", "line 2, column 1"},
		// Scanner errors ("offset <byte offset>: ...").
		{"line break in a basic string", "token = \"" + placeholder + "\n", "line 1, column 24"},
		{"invalid key rune", "a = 1\n  é = \"" + placeholder + "\"\n", "line 2, column 3"},
		{"unterminated multi-line string", "a = 1\nb = \"\"\"" + placeholder + "\n", "line 2, column 22"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, parseErr := tomledit.Parse(bytes.NewReader([]byte(tc.doc)))
			if parseErr == nil {
				t.Fatal("expected a parse error")
			}
			got := RedactTOMLEditError(parseErr, []byte(tc.doc)).Error()
			if want := "the document has a syntax error at " + tc.want; got != want {
				t.Errorf("got %q, want %q (tomledit: %q)", got, want, parseErr)
			}
			if strings.Contains(got, placeholder) {
				t.Errorf("redacted error quotes the document: %q", got)
			}
		})
	}
}

// An error in neither tomledit shape, or a position the document does not
// contain, gets the fixed reason rather than any part of the message.
func TestRedactTOMLEditErrorFallsBackToTheFixedReason(t *testing.T) {
	doc := []byte("a = 1\n")
	for _, msg := range []string{
		"sk-placeholder",
		"offset 999: invalid 'sk-placeholder'",
		"offset 0: invalid 'sk-placeholder'",
		"at 9:0: unexpected sk-placeholder",
		"at 0:0: unexpected sk-placeholder",
		"at 1:7: unexpected sk-placeholder",
		"at 1:99999999999999999999: unexpected sk-placeholder",
	} {
		got := RedactTOMLEditError(errors.New(msg), doc)
		if !errors.Is(got, errTOMLInvalid) {
			t.Errorf("%q: got %q, want the fixed reason", msg, got)
		}
	}
}

// tomledit's scanner does not count the line breaks inside a multi-line literal
// string, so a parser position after one names the wrong place; such a
// document gets the fixed reason rather than a plausible wrong position. The
// first case is a TOML 1.1 multi-line inline table, which BurntSushi accepts.
func TestRedactTOMLEditErrorDropsPositionsAfterAMultiLineLiteralString(t *testing.T) {
	for _, doc := range []string{
		"note = '''\nfirst\nsecond\n'''\n[env]\nX = { k = \"v\",\n  j = \"w\" }\n",
		"a = '''x\ny\nz'''\nb = c\n",
	} {
		_, parseErr := tomledit.Parse(bytes.NewReader([]byte(doc)))
		if parseErr == nil {
			t.Fatalf("%q: expected a parse error", doc)
		}
		if got := RedactTOMLEditError(parseErr, []byte(doc)); !errors.Is(got, errTOMLInvalid) {
			t.Errorf("%q: got %q, want the fixed reason (tomledit: %q)", doc, got, parseErr)
		}
	}
}
