package l10n

import (
	"errors"
	"fmt"
	"testing"
)

// testMessage is a minimal Message, as cmd's cmdError is one.
type testMessage struct {
	format  string
	args    []any
	english error
}

func newTestMessage(format string, args ...any) *testMessage {
	return &testMessage{format: format, args: args, english: fmt.Errorf(format, args...)}
}

func (m *testMessage) Error() string                  { return m.english.Error() }
func (m *testMessage) Unwrap() error                  { return errors.Unwrap(m.english) }
func (m *testMessage) MessageFormat() (string, []any) { return m.format, m.args }

// withJapanese selects Japanese with a catalog of entries for the rest of t.
func withJapanese(t *testing.T, entries map[string]string) {
	t.Helper()
	restore := UseCatalogForTest(entries)
	Set(Japanese)
	t.Cleanup(func() {
		Set(English)
		restore()
	})
}

func TestSprintfRendersTheSelectedLanguage(t *testing.T) {
	const format = "unknown tool: %s"
	if got := Sprintf(format, "zz"); got != "unknown tool: zz" {
		t.Fatalf("English: %q", got)
	}
	withJapanese(t, map[string]string{format: "不明なツールです: %s"})
	if got := Sprintf(format, "zz"); got != "不明なツールです: zz" {
		t.Fatalf("Japanese: %q", got)
	}
	if got := Sprintf("not in the catalog: %d", 3); got != "not in the catalog: 3" {
		t.Fatalf("a catalog miss must fall back to English: %q", got)
	}
}

func TestRenderLocalizesMessagesAndKeepsOtherErrorsVerbatim(t *testing.T) {
	external := errors.New("open x: permission denied")
	inner := newTestMessage("cannot read %s", "config.toml")
	outer := newTestMessage("load failed: %w", inner)
	wrapsExternal := newTestMessage("cannot write: %w", external)

	if got := Render(outer); got != "load failed: cannot read config.toml" {
		t.Fatalf("English render: %q", got)
	}
	withJapanese(t, map[string]string{
		"cannot read %s":   "%s を読めません",
		"load failed: %w":  "読み込みに失敗しました: %w",
		"cannot write: %w": "書き込めません: %w",
	})
	if got := Render(outer); got != "読み込みに失敗しました: config.toml を読めません" {
		t.Fatalf("a nested message renders localized: %q", got)
	}
	if got := Render(wrapsExternal); got != "書き込めません: open x: permission denied" {
		t.Fatalf("an external cause stays verbatim English: %q", got)
	}
	if got := Render(external); got != external.Error() {
		t.Fatalf("a non-message error renders verbatim: %q", got)
	}
	// The English text and the cause chain do not depend on the language.
	if outer.Error() != "load failed: cannot read config.toml" || !errors.Is(outer, inner) {
		t.Fatalf("Error() or Unwrap changed under Japanese: %q", outer.Error())
	}
	if !errors.Is(wrapsExternal, external) {
		t.Fatal("errors.Is must reach the external cause under Japanese")
	}
}

func TestRenderFallsBackToEnglishOnACatalogMiss(t *testing.T) {
	withJapanese(t, map[string]string{})
	m := newTestMessage("cannot read %s", "config.toml")
	if got := Render(m); got != "cannot read config.toml" {
		t.Fatalf("catalog miss: %q", got)
	}
}

func TestRenderOfANilErrorMatchesFmt(t *testing.T) {
	var typedNil *testMessage
	for _, err := range []error{nil, typedNil} {
		if got, want := Render(err), fmt.Sprint(err); got != want {
			t.Errorf("Render(%#v) = %q, fmt prints %q", err, got, want)
		}
	}
	withJapanese(t, map[string]string{"wraps %v": "包みます %v"})
	if got := Render(newTestMessage("wraps %v", error(typedNil))); got != "包みます <nil>" {
		t.Errorf("a nil message argument: %q", got)
	}
}
