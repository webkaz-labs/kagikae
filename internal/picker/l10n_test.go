package picker

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/webkaz-labs/kagikae/internal/testutil/l10ntest"
)

// A picker that cannot run fails with a message value: Error() keeps the English
// text, and a human sink renders the catalog's Japanese before the terminal
// library's own error, which stays verbatim and reachable through errors.Is.
func TestRunFailureRendersInJapanese(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := Run(ctx, strings.NewReader(""), io.Discard, []Item{{Label: "main", Value: "main"}}, Options{NoColor: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run on a cancelled context = %v, want it to wrap context.Canceled", err)
	}
	l10ntest.UseJapanese(t)
	l10ntest.ErrorText(t, "run", err, "picker: ", "ピッカー: ", true)
}

// Under Japanese the picker draws its placeholder, empty result and footer from
// the catalog; the wide placeholder is drawn whole, and cut by display width when
// the line is narrow.
func TestTextRendersInJapanese(t *testing.T) {
	l10ntest.UseJapanese(t)
	lines := func(m Model) []string { return strings.Split(m.View().Content, "\n") }

	open := lines(New(fixture(), Options{NoColor: true}))
	if got, want := open[0], "> 入力して絞り込み"; got != want {
		t.Errorf("filter line = %q, want %q", got, want)
	}
	if got, want := open[len(open)-1], "上下 移動   Enter 決定   Esc 絞り込み解除またはキャンセル"; got != want {
		t.Errorf("footer = %q, want %q", got, want)
	}
	if got, want := lines(press(New(fixture(), Options{NoColor: true}), typed("zzz")...))[1], "  一致する場所がありません"; got != want {
		t.Errorf("empty result = %q, want %q", got, want)
	}
	narrow := lines(press(New(fixture(), Options{NoColor: true}), tea.WindowSizeMsg{Width: 10, Height: 24}))
	if got, want := narrow[0], "> 入力して"; got != want {
		t.Errorf("filter line at 10 columns = %q, want %q", got, want)
	}
}
