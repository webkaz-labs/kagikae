package devtoolspolicy

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/webkaz-labs/kagikae/tools/devtools/glossary"
)

// The product selects Surface terms and Mechanism terms explicitly. Reading the
// real docs/CONTEXT.md checks that both selections still yield vocabulary after a
// heading edit; synthetic parser fixtures alone cannot detect that product drift.
func TestTheRealGlossaryYieldsTermsFromBothTables(t *testing.T) {
	got, err := glossary.Read(filepath.Join("..", "..", "docs", "CONTEXT.md"), map[string]bool{"Surface terms": true, "Mechanism terms": true})
	if err != nil {
		t.Fatalf("contextTerms on the real glossary: %v", err)
	}
	have := map[string]bool{}
	for _, g := range got {
		have[g] = true
	}
	// One term from each table, so a rename of either heading fails here.
	if !have["account"] {
		t.Errorf("no surface term found — has § Surface terms been renamed? got %v", got)
	}
	if !have["bound directory"] {
		t.Errorf("no mechanism term found — has § Mechanism terms been renamed? got %v", got)
	}
	// And the routing table's questions are not vocabulary.
	for _, g := range got {
		if strings.HasPrefix(g, "what ") {
			t.Errorf("contextTerms harvested a routing question: %q", g)
		}
	}
}
