// Package agy implements the detect-only Antigravity CLI adapter. Capture
// and switch refuse with unsupported until the full adapter lands
// (docs/ROADMAP.md Phase 5).
package agy

import (
	"context"
	"fmt"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

type Agy struct{}

func init() { adapter.Register(Agy{}) }

func (Agy) ID() string { return constants.ToolAgy }

func (Agy) Artifacts(_ context.Context, _ adapter.Env) ([]artifact.Spec, error) {
	return nil, fmt.Errorf("%w: the agy adapter is detect-only in this release (see docs/ROADMAP.md)", adapter.ErrUnsupported)
}

func (a Agy) Detect(_ context.Context, env adapter.Env) (adapter.Info, error) {
	info := adapter.Info{Tool: constants.ToolAgy, Warnings: []string{"agy adapter is detect-only (experimental)"}}
	if _, err := env.LookPath("agy"); err == nil {
		info.BinaryPresent = true
	}
	return info, nil
}

func (a Agy) Doctor(_ context.Context, env adapter.Env) []adapter.Check {
	tool := constants.ToolAgy
	return []adapter.Check{
		adapter.BinaryCheck(env, tool, "agy"),
		{Tool: tool, Code: constants.CheckUnsupported, Status: constants.StatusSkipped,
			Message: "agy auth switching is not implemented yet (detect-only)"},
	}
}
