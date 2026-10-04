package l10n

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestMsgRendersEnglishForErrorAndLocalizedForAHumanSink(t *testing.T) {
	restore := UseCatalogForTest(map[string]string{"run: kae use %s <account>": "kae use %s <account> を実行してください"})
	defer restore()
	m := Msgf("run: kae use %s <account>", "claude")
	Set(Japanese)
	defer Set(English)
	if got := m.Error(); got != "run: kae use claude <account>" {
		t.Errorf("Error() = %q, must stay English", got)
	}
	if got := Render(m); got != "kae use claude <account> を実行してください" {
		t.Errorf("Render = %q", got)
	}
	nested := Msgf("failed: %v", m)
	if got := Render(nested); got != "failed: run: kae use claude <account>" {
		// the parent has no catalog entry, so it renders English whole
		t.Errorf("Render(nested) = %q", got)
	}
}

func TestMsgEmpty(t *testing.T) {
	if !(Msg{}).Empty() || Msgf("x").Empty() {
		t.Fatal("only the zero Msg is empty")
	}
	if got := Render(Msg{}); got != "" {
		t.Errorf("the zero Msg renders %q", got)
	}
}

type wire struct {
	Message Msg    `json:"message"`
	Plain   string `json:"plain"`
}

// A Msg field encodes to the same bytes as the string field it replaces, under
// the encoder kae uses, which does not escape HTML. MarshalJSON would break this.
func TestMsgEncodesLikeAStringWithoutHTMLEscaping(t *testing.T) {
	text := "run: kae use <tool> <account> & more é \x01 \xff"
	encode := func(v any) []byte {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	got := encode(wire{Message: Msgf("%s", text), Plain: text})
	want := encode(struct {
		Message string `json:"message"`
		Plain   string `json:"plain"`
	}{text, text})
	if !bytes.Equal(got, want) {
		t.Errorf("Msg bytes differ from a string field:\n%s\nvs\n%s", got, want)
	}
	if bytes.Contains(got, []byte("\\u003c")) {
		t.Errorf("HTML was escaped: %s", got)
	}
}

func TestMsgIsAMessageAndNotComparableByEquality(t *testing.T) {
	var err error = Msgf("a")
	var m Message
	if !errors.As(err, &m) {
		t.Fatal("a Msg must be a Message")
	}
}

func TestOfCarriesAnErrorAsAMessage(t *testing.T) {
	restore := UseCatalogForTest(map[string]string{"inner %s": "内側 %s"})
	defer restore()
	Set(Japanese)
	defer Set(English)

	external := errors.New("open x: permission denied")
	if m := Of(external); m.Error() != "open x: permission denied" || Render(m) != "open x: permission denied" {
		t.Errorf("an external error is quoted verbatim: %q / %q", m.Error(), Render(m))
	}
	kae := Msgf("inner %s", "a")
	if m := Of(kae); m.Error() != "inner a" || Render(m) != "内側 a" {
		t.Errorf("a kae message keeps English for Error and localizes for a person: %q / %q", m.Error(), Render(m))
	}
	if m := Of(nil); !m.Empty() {
		t.Error("Of(nil) is the zero Msg")
	}
	if text, err := Of(external).MarshalText(); err != nil || string(text) != "open x: permission denied" {
		t.Errorf("MarshalText = %q, %v", text, err)
	}
}

// A plain Msg whose format is "%s" is not Unstopped: the kind, not the format,
// decides, so the argument's full stop stays.
func TestPlainPercentSMsgKeepsTheFullStop(t *testing.T) {
	withJapanese(t, map[string]string{"stopped": "止まりました。", "%s": "%s"})
	inner := Msgf("stopped")
	if got := Render(Msgf("%s", inner)); got != "止まりました。" {
		t.Fatalf("plain %%s Msg: %q", got)
	}
	if got := Render(Unstopped(inner)); got != "止まりました" {
		t.Fatalf("Unstopped: %q", got)
	}
}

// A List joins its items with ", " for Error() and JSON and with "、" for a
// Japanese human sink, alone or as a message's argument; an item is never split.
func TestListJoinsBySelectedLanguage(t *testing.T) {
	items := []string{"claude", "codex", "a, b"}
	l := List(items)
	items[0] = "changed" // List keeps its own copy
	parent := Msgf("tools: %s", l)
	if got := Render(parent); got != "tools: claude, codex, a, b" {
		t.Errorf("English Render = %q", got)
	}
	withJapanese(t, map[string]string{"tools: %s": "ツール: %s"})
	if got := Render(l); got != "claude、codex、a, b" {
		t.Errorf("Render = %q", got)
	}
	if got := Render(parent); got != "ツール: claude、codex、a, b" {
		t.Errorf("Render(parent) = %q", got)
	}
	if got := parent.Error(); got != "tools: claude, codex, a, b" {
		t.Errorf("Error() under Japanese = %q, must stay English", got)
	}
	if text, err := l.MarshalText(); err != nil || string(text) != "claude, codex, a, b" {
		t.Errorf("MarshalText = %q, %v", text, err)
	}
	if !List(nil).Empty() || Render(List(nil)) != "" {
		t.Error("an empty List is the zero Msg")
	}
}
