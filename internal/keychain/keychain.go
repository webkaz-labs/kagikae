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

// itemKey composes the cache key for a (service, account) lookup. An empty
// account is the account-agnostic, service-only form used by the
// single-item-per-service drivers (claude, cursor, codex). A non-empty account
// scopes the lookup to one item of a shared service — agy's gemini/antigravity,
// where the gemini service is also used by the Gemini ecosystem — so it must
// not share a cache entry with a service-only read.
func itemKey(service, account string) string {
	if account == "" {
		return service
	}
	return service + "\x00" + account
}

// ReadItem returns the generic-password payload for service (service-only
// match: the first item of that service). The security CLI prints non-ASCII
// payloads as hex; both forms are handled.
func ReadItem(ctx context.Context, service string) (payload []byte, found bool, err error) {
	return readItem(ctx, service, "")
}

// ReadItemForAccount returns the payload of the service item whose account
// attribute is account, so a shared service (agy's gemini, also used by the
// Gemini ecosystem) is read only at the kae-owned account (antigravity) and a
// sibling item under a different account is never read or touched.
func ReadItemForAccount(ctx context.Context, service, account string) (payload []byte, found bool, err error) {
	return readItem(ctx, service, account)
}

func readItem(ctx context.Context, service, account string) (payload []byte, found bool, err error) {
	key := itemKey(service, account)
	if c := cacheFrom(ctx); c != nil {
		if e, ok := c.lookupItem(key); ok {
			return e.payload, e.found, nil
		}
	}
	args := []string{"find-generic-password", "-s", service}
	if account != "" {
		args = append(args, "-a", account)
	}
	args = append(args, "-w")
	stdout, stderr, code := runner.Run(ctx, "security", args...)
	if code != 0 {
		if strings.Contains(stderr, NotFoundMarker) {
			if c := cacheFrom(ctx); c != nil {
				c.storeItem(key, itemEntry{found: false})
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
		c.storeItem(key, itemEntry{payload: payload, found: true})
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

// DeleteItem removes the generic password for service (service-only match). A
// missing item is not an error (the live artifact is already absent).
func DeleteItem(ctx context.Context, service string) error {
	return deleteItem(ctx, service, "")
}

// DeleteItemForAccount removes the service item whose account attribute is
// account, leaving any sibling item of the same service under a different
// account untouched (agy's gemini/antigravity, where the gemini service is
// shared with the Gemini ecosystem).
func DeleteItemForAccount(ctx context.Context, service, account string) error {
	return deleteItem(ctx, service, account)
}

func deleteItem(ctx context.Context, service, account string) error {
	args := []string{"delete-generic-password", "-s", service}
	if account != "" {
		args = append(args, "-a", account)
	}
	_, stderr, code := runner.Run(ctx, "security", args...)
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
