package adapter_test

import (
	"testing"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// Only codex compares owners. claude's recaptures are guarded by its
// identity-only artifact instead; a tool that implemented the comparison by
// accident would have its recaptures declined on its answer.
func TestOnlyCodexIsAnOwnerComparer(t *testing.T) {
	for _, tool := range constants.Tools {
		a, err := adapter.ForTool(tool)
		if err != nil {
			t.Fatal(err)
		}
		_, compares := a.(adapter.OwnerComparer)
		if compares != (tool == constants.ToolCodex) {
			t.Errorf("%s implements OwnerComparer = %v", tool, compares)
		}
	}
}
