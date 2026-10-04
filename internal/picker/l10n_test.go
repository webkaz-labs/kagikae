package picker

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

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
