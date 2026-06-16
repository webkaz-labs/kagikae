// Package jwt decodes the claims segment of a JWT. It is a best-effort reader
// for credential introspection (freshness expiry, codex account identity), not
// a verifier: kae never validates a JWT signature, it only reads claims kae
// already trusts because they came from the live credential store.
package jwt

import (
	"encoding/base64"
	"strings"
)

// Payload returns the raw (base64url-decoded) claims segment of a JWT, or
// ok=false when token is not a well-formed three-segment JWT. Both the
// unpadded (RawURLEncoding, per RFC 7519) and padded encodings are accepted.
func Payload(token string) (claims []byte, ok bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		if decoded, err = base64.URLEncoding.DecodeString(parts[1]); err != nil {
			return nil, false
		}
	}
	return decoded, true
}
