// Package freshness extracts credential expiry and refresh-token presence from
// a captured auth payload, per tool. It is a pure parser: no IO, no mutation.
// The switch-time stale warning (docs/RELEASE.md §B) and doctor
// credential-health (§D) share it so they apply one predicate.
//
// Not every tool exposes a datable credential. claude/codex/opencode/cursor
// authenticate with a refreshable OAuth/JWT token whose expiry kae can read;
// copilot's switched artifact is only the active-account pointer (the dated
// tokens live in untouched keychain items) and agy's is an opaque encrypted
// blob, so both report Known=false. A static API key never reaches this path —
// it is env-mode, not a snapshot.
package freshness

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/webkaz-labs/kagikae/internal/constants"
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

// Inspect reads the freshness of one captured credential payload for tool.
func Inspect(tool string, payload []byte) Info {
	switch tool {
	case constants.ToolClaude:
		return inspectClaude(payload)
	case constants.ToolCodex:
		return inspectCodex(payload)
	case constants.ToolOpencode:
		return inspectOpencode(payload)
	case constants.ToolCursor:
		return inspectCursor(payload)
	default:
		return Info{} // copilot pointer, agy blob: not datable
	}
}

// inspectClaude reads claudeAiOauth's expiresAt (Unix ms) and refreshToken. The
// keychain payload wraps the object under claudeAiOauth; the file-driver
// snapshot stores the inner object directly, so both nestings are handled.
func inspectClaude(payload []byte) Info {
	root, ok := decodeObject(payload)
	if !ok {
		return Info{}
	}
	obj := root
	if inner, ok := root["claudeAiOauth"]; ok {
		if nested, ok := decodeObject(inner); ok {
			obj = nested
		}
	}
	return Info{
		Known:      true,
		ExpiresAt:  epochToTime(numberFrom(obj["expiresAt"])),
		HasRefresh: nonEmptyString(obj["refreshToken"]),
	}
}

// inspectOpencode reads the /openai sub-value {type, refresh, access, expires}.
func inspectOpencode(payload []byte) Info {
	obj, ok := decodeObject(payload)
	if !ok {
		return Info{}
	}
	return Info{
		Known:      true,
		ExpiresAt:  epochToTime(numberFrom(obj["expires"])),
		HasRefresh: nonEmptyString(obj["refresh"]),
	}
}

// inspectCodex reads tokens.refresh_token presence and the access (or id) token
// JWT expiry from a whole auth.json. A file holding only OPENAI_API_KEY parses
// as Known with no expiry.
func inspectCodex(payload []byte) Info {
	var doc struct {
		Tokens struct {
			AccessToken  string `json:"access_token"`
			IDToken      string `json:"id_token"`
			RefreshToken string `json:"refresh_token"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		return Info{}
	}
	info := Info{Known: true, HasRefresh: doc.Tokens.RefreshToken != ""}
	if exp, ok := jwtExpiry(doc.Tokens.AccessToken); ok {
		info.ExpiresAt = exp
	} else if exp, ok := jwtExpiry(doc.Tokens.IDToken); ok {
		info.ExpiresAt = exp
	}
	return info
}

// inspectCursor reads the expiry of cursor's opaque raw-JWT credential. There
// is no refresh token (the JWT is the whole credential).
func inspectCursor(payload []byte) Info {
	if exp, ok := jwtExpiry(strings.TrimSpace(string(payload))); ok {
		return Info{Known: true, ExpiresAt: exp}
	}
	return Info{}
}

// jwtExpiry decodes a JWT's claims and returns its exp (seconds since epoch).
func jwtExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	claimBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		if claimBytes, err = base64.URLEncoding.DecodeString(parts[1]); err != nil {
			return time.Time{}, false
		}
	}
	var claims struct {
		Exp float64 `json:"exp"`
	}
	if err := json.Unmarshal(claimBytes, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(claims.Exp), 0).UTC(), true
}

func decodeObject(raw []byte) (map[string]json.RawMessage, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, false
	}
	return obj, true
}

func numberFrom(raw json.RawMessage) float64 {
	var n float64
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	return 0
}

func nonEmptyString(raw json.RawMessage) bool {
	var s string
	return json.Unmarshal(raw, &s) == nil && s != ""
}

// epochToTime converts a numeric expiry to a time. Millisecond-scale values
// (>= 1e12 for any plausible recent date) are treated as Unix ms; smaller ones
// as Unix seconds. A non-positive value is "no expiry".
func epochToTime(n float64) time.Time {
	if n <= 0 {
		return time.Time{}
	}
	if n >= 1e12 {
		return time.UnixMilli(int64(n)).UTC()
	}
	return time.Unix(int64(n), 0).UTC()
}
