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
