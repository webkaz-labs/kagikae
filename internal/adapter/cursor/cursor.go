// Package cursor implements the Cursor CLI (cursor-agent) adapter. Auth mode
// switches the single macOS Keychain item that holds the access token; the
// payload is an opaque raw JWT (not JSON), captured and restored verbatim.
// Linux credential storage is undocumented, so the adapter is darwin-only for
// now (see docs/ADAPTERS.md and docs/ROADMAP.md).
package cursor

import (
	"context"
	"fmt"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/adapter"
	"github.com/webkaz-labs/kagikae/internal/artifact"
	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/freshness"
	"github.com/webkaz-labs/kagikae/internal/runner"
)

// binaryName is the Cursor CLI executable; KeychainService is the access-token
// item's service name; KeychainAccount is the account attribute cursor-agent
// creates it with.
const (
	binaryName      = "cursor-agent"
	KeychainService = "cursor-access-token"
	KeychainAccount = "cursor-user"
)

type Cursor struct{}

func init() { adapter.Register(Cursor{}) }

func (Cursor) ID() string { return constants.ToolCursor }

func (Cursor) Binary() string { return binaryName }

// driver maps the platform to the cursor driver, refusing the platforms whose
// credential storage is undocumented (only macOS Keychain is known). Mirrors
// claude's driver() so Artifacts/Doctor share one platform gate.
func driver(env adapter.Env) (string, error) {
	if env.GOOS == "darwin" {
		return constants.DriverCursorKeychain, nil
	}
	return "", fmt.Errorf("%w: cursor auth switching is not supported on %s yet (only macOS Keychain storage is known)",
		adapter.ErrUnsupported, env.GOOS)
}

func (c Cursor) Artifacts(_ context.Context, env adapter.Env) ([]artifact.Spec, error) {
	if _, err := driver(env); err != nil {
		return nil, err
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
	if _, err := env.LookPath(binaryName); err == nil {
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

// statusLoginMarker precedes the email in `cursor-agent status` output.
const statusLoginMarker = "Logged in as "

// Identity reads the logged-in email from `cursor-agent status` so
// `kae add cursor` (no name) can default the account name. Discovery 2026-06-16:
// the command prints a single line `✓ Logged in as <email>` (UTF-8 check glyph,
// no ANSI, exit 0); kae extracts the text after "Logged in as " and lets cmd
// sanitize the email to a local-part account name. A non-zero exit, a line
// without the marker, or an empty identity is a detection failure (cmd then
// names the explicit form). cursor-agent status may hit the network — acceptable
// on the interactive `kae add` path. Runs through the runner seam.
func (Cursor) Identity(ctx context.Context, _ adapter.Env) (string, error) {
	stdout, stderr, code := runner.Run(ctx, binaryName, "status")
	if code != 0 {
		return "", fmt.Errorf("cursor-agent status failed (exit %d): %s", code, runner.Snippet(stderr))
	}
	_, rest, found := strings.Cut(stdout, statusLoginMarker)
	if !found {
		return "", fmt.Errorf("cursor-agent status did not report a logged-in account")
	}
	if nl := strings.IndexAny(rest, "\r\n"); nl >= 0 {
		rest = rest[:nl] // the identity is the remainder of that line
	}
	identity := strings.TrimSpace(rest)
	if identity == "" {
		return "", fmt.Errorf("cursor-agent status reported an empty account")
	}
	return identity, nil
}

// Freshness reads the expiry of cursor's opaque raw-JWT credential. There is no
// refresh token (the JWT is the whole credential), so an expired snapshot
// always warns rather than self-refreshing.
func (Cursor) Freshness(payload []byte) freshness.Info {
	if exp, ok := freshness.JWTExpiry(strings.TrimSpace(string(payload))); ok {
		return freshness.Info{Known: true, ExpiresAt: exp}
	}
	return freshness.Info{}
}

func (c Cursor) Doctor(ctx context.Context, env adapter.Env) []adapter.Check {
	tool := constants.ToolCursor
	if _, err := driver(env); err != nil {
		return []adapter.Check{{Tool: tool, Code: constants.CheckUnsupported,
			Status: constants.StatusError, Message: err.Error()}}
	}
	checks := []adapter.Check{adapter.BinaryCheck(env, tool, binaryName)}
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
