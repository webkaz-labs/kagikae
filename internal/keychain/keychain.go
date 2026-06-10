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
	stdout, stderr, code := runner.Run(ctx, "security",
		"find-generic-password", "-s", service, "-w")
	if code != 0 {
		if strings.Contains(stderr, NotFoundMarker) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("security find-generic-password %q failed (exit %d)", service, code)
	}
	raw := strings.TrimRight(stdout, "\n")
	if decoded, ok := decodeHexPayload(raw); ok {
		return decoded, true, nil
	}
	return []byte(raw), true, nil
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
	stdout, stderr, code := runner.Run(ctx, "security",
		"find-generic-password", "-s", service)
	if code != 0 {
		if strings.Contains(stderr, NotFoundMarker) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("security find-generic-password %q failed (exit %d)", service, code)
	}
	m := acctRE.FindStringSubmatch(stdout)
	if m == nil {
		return "", true, nil
	}
	return m[1], true, nil
}

// WriteItem creates or updates (-U) the generic password for service.
func WriteItem(ctx context.Context, service, account string, payload []byte) error {
	_, stderr, code := runner.Run(ctx, "security",
		"add-generic-password", "-U", "-s", service, "-a", account, "-w", string(payload))
	if code != 0 {
		return fmt.Errorf("security add-generic-password %q failed (exit %d): %s", service, code, runner.Snippet(stderr))
	}
	return nil
}
