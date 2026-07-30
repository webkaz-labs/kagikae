// Package secret stores kagikae's captured credential payloads. The default
// backends are OS credential stores (macOS Keychain via the security CLI,
// Linux libsecret via secret-tool); a plaintext file backend exists as an
// explicit opt-in. Payloads are base64-encoded inside every backend so CLI
// round-trips stay binary-safe and printable.
package secret

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// Service is the credential-store service name for kagikae's own entries.
const Service = "kagikae"

// Backend names (config tokens).
const (
	BackendAuto      = "auto"
	BackendKeychain  = "keychain"
	BackendLibsecret = "libsecret"
	BackendFile      = "file"
)

// ErrUnavailable means no usable secret backend exists; the caller maps it
// to exit code 9 with guidance.
var ErrUnavailable = errors.New("secret store unavailable")

// Backend stores and retrieves secret payloads by key. Keys look like
// "claude/main/oauth_account" or "backup/<id>/claude/oauth_account".
type Backend interface {
	Name() string
	Get(ctx context.Context, key string) (value []byte, found bool, err error)
	Set(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
}

// Enumerator is implemented by backends that can list every stored kagikae key
// (account refs like "claude/main/claude_ai_oauth" and backup refs like
// "backup/<id>/claude/..."). doctor uses it for orphan detection. The darwin
// keychain cannot enumerate by service through the security CLI, so
// keychainBackend does not implement it and orphan detection is skipped there
// (a documented gap; docs/SECURITY.md).
type Enumerator interface {
	Keys(ctx context.Context) ([]string, error)
}

// Reserved first segments of a stored key. Every namespace but the account one
// prefixes its keys with one of these, so an account key is recognized by the
// absence of a prefix. The three builders that own those namespaces
// (backup.SecretRef, companion.SecretRef, envprofile.SecretRef) compose their
// keys from these constants, so this list cannot drift away from the key shapes
// it describes.
const (
	NSBackup    = "backup"
	NSCompanion = "companion"
	NSEnv       = "env"
)

// AccountKey reports whether key belongs to the account namespace
// (<tool>/<account>/<artifact>, built by account.SecretRef) and returns its
// tool and account. Every other namespace returns ok=false: those keys have no
// snapshot dir behind them, and reading one as <tool>/<account> is what made
// doctor's orphan check warn forever on every companion binding and every
// env-profile variable.
func AccountKey(key string) (tool, account string, ok bool) {
	parts := strings.Split(key, "/")
	if len(parts) != 3 {
		return "", "", false // backup/companion/env keys carry four segments
	}
	switch parts[0] {
	case NSBackup, NSCompanion, NSEnv:
		return "", "", false
	}
	if parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// Resolve picks the backend for the configured name. lookPath is an
// exec.LookPath-compatible probe injected for tests.
func Resolve(configured, goos, secretsDir string, lookPath func(string) (string, error)) (Backend, error) {
	switch configured {
	case BackendKeychain:
		if goos != "darwin" {
			return nil, fmt.Errorf("%w: keychain backend requires macOS", ErrUnavailable)
		}
		return keychainBackend{}, nil
	case BackendLibsecret:
		if _, err := lookPath("secret-tool"); err != nil {
			return nil, fmt.Errorf("%w: secret-tool not found in PATH (install libsecret tools)", ErrUnavailable)
		}
		return libsecretBackend{}, nil
	case BackendFile:
		return fileBackend{dir: secretsDir}, nil
	case BackendAuto, "":
		if goos == "darwin" {
			return keychainBackend{}, nil
		}
		if _, err := lookPath("secret-tool"); err == nil {
			return libsecretBackend{}, nil
		}
		return nil, fmt.Errorf("%w: no OS credential store found; install libsecret tools or opt in with security.secret_backend = \"file\"", ErrUnavailable)
	default:
		return nil, fmt.Errorf("unknown secret_backend %q", configured)
	}
}

// encodePayload/decodePayload are the single place defining how payloads are
// wrapped for storage (base64, so every backend round-trips printable ASCII).
func encodePayload(value []byte) string {
	return base64.StdEncoding.EncodeToString(value)
}

func decodePayload(backendName, key, stored string) ([]byte, error) {
	value, err := base64.StdEncoding.DecodeString(strings.TrimSpace(stored))
	if err != nil {
		return nil, fmt.Errorf("%s entry %s is not kagikae-encoded: %w", backendName, key, err)
	}
	return value, nil
}

// validateKey rejects keys that could escape storage namespaces.
func validateKey(key string) error {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "..") {
		return fmt.Errorf("invalid secret key %q", key)
	}
	for _, part := range strings.Split(key, "/") {
		if part == "" {
			return fmt.Errorf("invalid secret key %q", key)
		}
	}
	return nil
}
