// Package cursor implements the Cursor CLI (cursor-agent) adapter. Auth mode
// switches the single macOS Keychain item that holds the access token; the
// payload is an opaque raw JWT (not JSON), captured and restored verbatim.
// Linux credential storage is undocumented, so the adapter is darwin-only for
// now (see docs/ADAPTERS.md and docs/ROADMAP.md).
package cursor

import (
	"context"
	"fmt"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
)

// KeychainService is the access-token item's service name; KeychainAccount
// is the account attribute cursor-agent creates it with.
const (
	KeychainService = "cursor-access-token"
	KeychainAccount = "cursor-user"
)

type Cursor struct{}

func init() { adapter.Register(Cursor{}) }

func (Cursor) ID() string { return constants.ToolCursor }

// unsupported reports the darwin-only limitation as an ErrUnsupported (exit 5).
func unsupported(goos string) error {
	return fmt.Errorf("%w: cursor auth switching is not supported on %s yet (only macOS Keychain storage is known)",
		adapter.ErrUnsupported, goos)
}

func (c Cursor) Artifacts(_ context.Context, env adapter.Env) ([]artifact.Spec, error) {
	if env.GOOS != "darwin" {
		return nil, unsupported(env.GOOS)
	}
	return []artifact.Spec{{
		Name:            "access_token",
		Kind:            constants.KindKeychain,
		Target:          KeychainService,
		Pointer:         "", // opaque: the payload is a raw JWT, not JSON
		KeychainAccount: KeychainAccount,
	}}, nil
}

func (c Cursor) Detect(ctx context.Context, env adapter.Env) (adapter.Info, error) {
	info := adapter.Info{Tool: constants.ToolCursor, Driver: constants.DriverCursorKeychain, Warnings: []string{}}
	if _, err := env.LookPath("cursor-agent"); err == nil {
		info.BinaryPresent = true
	}
	specs, err := c.Artifacts(ctx, env)
	if err != nil {
		return info, err
	}
	v, err := artifact.ReadLive(ctx, specs[0])
	if err != nil {
		return info, err
	}
	info.AuthPresent = v.Present
	return info, nil
}

func (c Cursor) Doctor(ctx context.Context, env adapter.Env) []adapter.Check {
	tool := constants.ToolCursor
	if env.GOOS != "darwin" {
		return []adapter.Check{{Tool: tool, Code: constants.CheckUnsupported,
			Status: constants.StatusError, Message: unsupported(env.GOOS).Error()}}
	}
	checks := []adapter.Check{adapter.BinaryCheck(env, tool, "cursor-agent")}
	info, err := c.Detect(ctx, env)
	switch {
	case err != nil:
		checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckAuthPresent,
			Status: constants.StatusError, Message: err.Error()})
	case info.AuthPresent:
		checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckAuthPresent,
			Status: constants.StatusOK, Message: "access token found in the keychain"})
	default:
		checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckAuthPresent,
			Status: constants.StatusWarn, Message: "no access token in the keychain; log in with `cursor-agent login` first"})
	}
	checks = append(checks, adapter.Check{Tool: tool, Code: constants.CheckDriver,
		Status: constants.StatusOK, Message: "driver: " + constants.DriverCursorKeychain})
	return checks
}
