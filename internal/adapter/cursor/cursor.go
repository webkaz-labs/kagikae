// Package cursor implements the Cursor CLI (cursor-agent) adapter. Auth mode
// switches the three macOS Keychain items cursor-agent writes as one unit; each
// payload is an opaque token (not JSON), captured and restored verbatim.
// Linux credential storage is now known but unimplemented, so the adapter stays
// darwin-only (see docs/ADAPTERS.md and docs/ROADMAP.md).
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

// binaryName is the Cursor CLI executable. cursor-agent builds every keychain
// name below from one credential "domain" it holds as a build-time constant
// ("cursor" in a release build, "cursor-dev" in a dev build) —
// `<domain>-access-token`, `<domain>-refresh-token`, `<domain>-api-key`, under
// account `<domain>-user`. Unlike claude's and codex's, that is a constant and not
// a rule (nothing in the environment moves it), which is why kae may spell the
// resulting names out.
//
// The three services are one credential: cursor-agent's setAuthentication writes
// access + refresh (+ api key, when the login had one) together, and its
// clearAuthentication deletes all three. Switching a subset leaves a mixed pair
// (docs/ADAPTERS.md § Cursor).
const (
	binaryName             = "cursor-agent"
	KeychainService        = "cursor-access-token"
	KeychainServiceRefresh = "cursor-refresh-token"
	KeychainServiceAPIKey  = "cursor-api-key"
	KeychainAccount        = "cursor-user"
)

type Cursor struct{}

func init() { adapter.Register(Cursor{}) }

func (Cursor) ID() string { return constants.ToolCursor }

func (Cursor) Binary() string { return binaryName }

// VerifiedVersion is empty for cursor, so doctor never reports upstream_version
// for it. cursor-agent is date-versioned (`2026.06.16-20-30-07-<sha>`), so the
// major/minor comparison reads the *month* as the minor: the first build of any
// new month is "past" the verified one, and doctor would then warn every month
// until a human edited a constant. A monthly nag about a daily-built tool is the
// exact failure the patch-bump silence exists to avoid — a false warning trains
// the user to ignore the real ones. The verified date is recorded in
// docs/VALIDATION.md "Upstream Behaviour Assumptions" instead, which is where the
// re-verification actually happens.
func (Cursor) VerifiedVersion() string { return "" }

// VerifiedOn is when those assumptions were last checked (docs/VALIDATION.md).
func (Cursor) VerifiedOn() string { return "2026-07-30" }

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

// Artifacts returns the access token first, then the refresh token, then the API
// key. Detect reads specs[0] as the credential whose presence means "logged in",
// so the order is a contract of this adapter, pinned by
// TestCursorArtifactsDarwinOpaqueKeychain.
//
// All three are credentials, never IdentityOnly: an absent one is applied as
// absent (the item is removed), which is what keeps a switch from leaving the
// previous account's token behind. The API key is normally absent — only an
// api-key login creates it — and switching it is what closes the one silent
// wrong-account path measured here: with an API key present, cursor-agent
// re-mints an expiring access token from it and writes all three items back, so
// an unswitched API key silently restores the previous account.
func (c Cursor) Artifacts(_ context.Context, env adapter.Env) ([]artifact.Spec, error) {
	if _, err := driver(env); err != nil {
		return nil, err
	}
	// Pointer is empty on all three: the payloads are opaque tokens, not JSON.
	spec := func(name, service string) artifact.Spec {
		return artifact.Spec{
			Name:            name,
			Kind:            constants.KindKeychain,
			Target:          service,
			Pointer:         "",
			KeychainAccount: KeychainAccount,
			// The account is a build-time constant cursor-agent reads by, so scope
			// to it: an item under any other account is not cursor's and must
			// neither be captured nor overwritten.
			KeychainMatchAccount: true,
		}
	}
	return []artifact.Spec{
		spec("access_token", KeychainService),
		spec("refresh_token", KeychainServiceRefresh),
		spec("api_key", KeychainServiceAPIKey),
	}, nil
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

// Freshness reads the expiry of cursor's opaque raw-JWT access token, so an
// expired snapshot warns rather than being treated as self-refreshing.
//
// A refresh token **does** exist (kae switches it), and this still holds — but for
// a measured reason, not because the JWT is the whole credential: cursor-agent
// never redeems the stored refresh token. Its only path to a new access token
// exchanges an **API key** (`cursor-api-key`, else `CURSOR_API_KEY`) at
// /auth/exchange_user_api_key; with no API key an expiring token is returned
// as-is and the request fails. The `grant_type=refresh_token` code in the bundle
// belongs to the MCP client's own OAuth, not to cursor's login. Measured against
// cursor-agent 2026.06.16 (docs/VALIDATION.md § Upstream Behaviour Assumptions);
// if a release starts redeeming it, this becomes "refreshable" and the warning has
// to learn about the refresh token.
//
// Called with one artifact payload at a time and with no idea which artifact it
// is, so the *set* has a tie-break: cmd.accountFreshness takes the first artifact
// that answers, in sorted-name order, and "access_token" < "api_key" <
// "refresh_token". Nothing here can tell an access token from another JWT — if a
// release ever shipped a JWT-shaped refresh token, that order is the only thing
// choosing between them — so the contract is pinned by
// cmd.TestCursorFreshnessComesFromTheAccessToken rather than left to the alphabet.
func (Cursor) Freshness(payload []byte) freshness.Info {
	if exp, ok := freshness.JWTExpiry(strings.TrimSpace(string(payload))); ok {
		return freshness.Info{Known: true, ExpiresAt: exp}
	}
	return freshness.Info{}
}

func (c Cursor) Doctor(ctx context.Context, env adapter.Env) []adapter.Check {
	tool := constants.ToolCursor
	if _, err := driver(env); err != nil {
		return []adapter.Check{{
			Tool: tool, Code: constants.CheckUnsupported,
			Status: constants.StatusError, Message: err.Error(),
		}}
	}
	checks := []adapter.Check{adapter.BinaryCheck(env, tool, binaryName)}
	info, err := c.Detect(ctx, env)
	switch {
	case err != nil:
		checks = append(checks, adapter.Check{
			Tool: tool, Code: constants.CheckAuthPresent,
			Status: constants.StatusError, Message: err.Error(),
		})
	case info.AuthPresent:
		checks = append(checks, adapter.Check{
			Tool: tool, Code: constants.CheckAuthPresent,
			Status: constants.StatusOK, Message: "access token found in the keychain",
		})
	default:
		checks = append(checks, adapter.Check{
			Tool: tool, Code: constants.CheckAuthPresent,
			Status: constants.StatusWarn, Message: "no access token in the keychain; log in with `cursor-agent login` first",
		})
	}
	checks = append(checks, adapter.Check{
		Tool: tool, Code: constants.CheckDriver,
		Status: constants.StatusOK, Message: "driver: " + constants.DriverCursorKeychain,
	})
	return checks
}
