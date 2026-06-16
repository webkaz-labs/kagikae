// Package freshness holds the primitives for reading credential expiry and
// refresh-token presence from a captured auth payload. It is a pure parser
// library: no IO, no mutation, no per-tool knowledge.
//
// The per-tool logic that turns a payload into an Info lives on each tool's
// adapter (adapter.Fresher), so per-tool credential knowledge has one home (the
// registry). Adapters build their Info from the shared primitives here
// (JWTExpiry / EpochToTime / DecodeObject / NumberFrom / NonEmptyString) plus
// internal/jwt. cmd dispatches to the adapter (cmd.freshnessOf); a tool with no
// Fresher method is treated as not-datable (Known=false).
//
// Not every tool exposes a datable credential. claude/codex/opencode/cursor
// authenticate with a refreshable OAuth/JWT token whose expiry kae can read;
// copilot's switched artifact is only the active-account pointer (the dated
// tokens live in untouched keychain items) and agy's is an opaque encrypted
// blob, so both report Known=false. A static API key never reaches this path —
// it is env-mode, not a snapshot.
package freshness

import (
	"encoding/json"
	"time"

	"github.com/webkaz-labs/kagikae/internal/jwt"
)

// Info is what a credential payload reveals about its freshness.
type Info struct {
	// Known is true when the payload parsed as the tool's recognized credential
	// format. A false Known means kae cannot judge staleness (opaque or
	// pointer-only payload, or unparseable bytes).
	Known bool
	// ExpiresAt is the access token's expiry. Zero when the format is known but
	// carries no expiry (e.g. a codex auth.json holding only an API key).
	ExpiresAt time.Time
	// HasRefresh is true when a non-empty refresh token is present, so the tool
	// can self-refresh an expired access token without a re-login.
	HasRefresh bool
}

// JWTExpiry decodes a JWT's claims and returns its exp (seconds since epoch).
// kae never verifies the signature; it reads a claim it already trusts because
// the token came from the live credential store.
func JWTExpiry(token string) (time.Time, bool) {
	claimBytes, ok := jwt.Payload(token)
	if !ok {
		return time.Time{}, false
	}
	var claims struct {
		Exp float64 `json:"exp"`
	}
	if err := json.Unmarshal(claimBytes, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(claims.Exp), 0).UTC(), true
}

// DecodeObject unmarshals raw into a JSON object, or ok=false when raw is not a
// JSON object.
func DecodeObject(raw []byte) (map[string]json.RawMessage, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, false
	}
	return obj, true
}

// NumberFrom reads raw as a JSON number, or 0 when it is absent or not numeric.
func NumberFrom(raw json.RawMessage) float64 {
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	return 0
}

// NonEmptyString reports whether raw is a non-empty JSON string.
func NonEmptyString(raw json.RawMessage) bool {
	var s string
	return json.Unmarshal(raw, &s) == nil && s != ""
}

// EpochToTime converts a numeric expiry to a time. Millisecond-scale values
// (>= 1e12 for any plausible recent date) are treated as Unix ms; smaller ones
// as Unix seconds. A non-positive value is "no expiry".
func EpochToTime(n float64) time.Time {
	if n <= 0 {
		return time.Time{}
	}
	if n >= 1e12 {
		return time.UnixMilli(int64(n)).UTC()
	}
	return time.Unix(int64(n), 0).UTC()
}
