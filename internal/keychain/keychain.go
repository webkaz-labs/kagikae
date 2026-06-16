// Package keychain accesses upstream tools' macOS Keychain items (such as
// Claude Code's credential item) through the security CLI via the runner
// seam. kagikae's own secret storage lives in internal/secret; this package
// is only for reading/patching items other tools own.
package keychain

import (
	"context"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/runner"
)

var acctRE = regexp.MustCompile(`"acct"<blob>="((?:[^"\\]|\\.)*)"`)

// NotFoundMarker is the stable substring the security CLI prints on stderr
// when a generic-password item does not exist. internal/secret reuses it.
const NotFoundMarker = "could not be found"

// ReadItem returns the generic-password payload for service. The security
// CLI prints non-ASCII payloads as hex; both forms are handled.
func ReadItem(ctx context.Context, service string) (payload []byte, found bool, err error) {
	if c := cacheFrom(ctx); c != nil {
		if e, ok := c.lookupItem(service); ok {
			return e.payload, e.found, nil
		}
	}
	stdout, stderr, code := runner.Run(ctx, "security",
		"find-generic-password", "-s", service, "-w")
	if code != 0 {
		if strings.Contains(stderr, NotFoundMarker) {
			if c := cacheFrom(ctx); c != nil {
				c.storeItem(service, itemEntry{found: false})
			}
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("security find-generic-password %q failed (exit %d)", service, code)
	}
	raw := strings.TrimRight(stdout, "\n")
	payload = []byte(raw)
	if decoded, ok := decodeHexPayload(raw); ok {
		payload = decoded
	}
	if c := cacheFrom(ctx); c != nil {
		c.storeItem(service, itemEntry{payload: payload, found: true})
	}
	return payload, true, nil
}

// decodeHexPayload detects the security CLI's hex output form.
func decodeHexPayload(s string) ([]byte, bool) {
	if len(s) == 0 || len(s)%2 != 0 {
		return nil, false
	}
	for _, r := range s {
		isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
		if !isHex {
			return nil, false
		}
	}
	decoded, err := hex.DecodeString(s)
	if err != nil {
		return nil, false
	}
	// Heuristic: upstream credential payloads are JSON. A plain-text password
	// that merely looks hexadecimal would not start with '{'.
	if len(decoded) == 0 || decoded[0] != '{' {
		return nil, false
	}
	return decoded, true
}

// ItemAccount returns the account attribute of the service's item.
func ItemAccount(ctx context.Context, service string) (string, bool, error) {
	if c := cacheFrom(ctx); c != nil {
		if e, ok := c.lookupAccount(service); ok {
			return e.account, e.found, nil
		}
	}
	stdout, stderr, code := runner.Run(ctx, "security",
		"find-generic-password", "-s", service)
	if code != 0 {
		if strings.Contains(stderr, NotFoundMarker) {
			if c := cacheFrom(ctx); c != nil {
				c.storeAccount(service, acctEntry{found: false})
			}
			return "", false, nil
		}
		return "", false, fmt.Errorf("security find-generic-password %q failed (exit %d)", service, code)
	}
	account := ""
	if m := acctRE.FindStringSubmatch(stdout); m != nil {
		account = m[1]
	}
	if c := cacheFrom(ctx); c != nil {
		c.storeAccount(service, acctEntry{account: account, found: true})
	}
	return account, true, nil
}

// WriteItem creates or updates (-U) the generic password for service.
//
// Callers must pass the payload exactly as the owning tool wrote it: Claude
// Code stores compact JSON and refuses a re-serialized (pretty-printed or
// key-sorted) payload even when it is semantically identical, so kagikae
// preserves the captured bytes verbatim instead of round-tripping through a
// JSON encoder. The write must go through this `security` CLI path (not the
// Security.framework API directly): `/usr/bin/security` is in the item's ACL
// trusted-application list, so the owning tool can still read the item
// afterwards without a keychain prompt.
func WriteItem(ctx context.Context, service, account string, payload []byte) error {
	_, stderr, code := runner.Run(ctx, "security",
		"add-generic-password", "-U", "-s", service, "-a", account, "-w", string(payload))
	if code != 0 {
		return fmt.Errorf("security add-generic-password %q failed (exit %d): %s", service, code, runner.Snippet(stderr))
	}
	if c := cacheFrom(ctx); c != nil {
		c.invalidate(service)
	}
	return nil
}

// DeleteItem removes the generic password for service. A missing item is not
// an error (the live artifact is already absent).
func DeleteItem(ctx context.Context, service string) error {
	_, stderr, code := runner.Run(ctx, "security",
		"delete-generic-password", "-s", service)
	if code != 0 {
		if strings.Contains(stderr, NotFoundMarker) {
			if c := cacheFrom(ctx); c != nil {
				c.invalidate(service)
			}
			return nil
		}
		return fmt.Errorf("security delete-generic-password %q failed (exit %d): %s", service, code, runner.Snippet(stderr))
	}
	if c := cacheFrom(ctx); c != nil {
		c.invalidate(service)
	}
	return nil
}
