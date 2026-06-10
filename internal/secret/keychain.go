package secret

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/runner"
)

// keychainBackend stores entries as macOS Keychain generic passwords:
// service "kagikae", account = key. Values are base64 so the security CLI
// always round-trips them as printable ASCII.
type keychainBackend struct{}

func (keychainBackend) Name() string { return BackendKeychain }

func (keychainBackend) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if err := validateKey(key); err != nil {
		return nil, false, err
	}
	stdout, stderr, code := runner.Run(ctx, "security",
		"find-generic-password", "-s", Service, "-a", key, "-w")
	if code != 0 {
		if strings.Contains(stderr, "could not be found") {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("security find-generic-password failed (exit %d)", code)
	}
	value, err := base64.StdEncoding.DecodeString(strings.TrimSpace(stdout))
	if err != nil {
		return nil, false, fmt.Errorf("keychain entry %s is not kagikae-encoded: %w", key, err)
	}
	return value, true, nil
}

func (keychainBackend) Set(ctx context.Context, key string, value []byte) error {
	if err := validateKey(key); err != nil {
		return err
	}
	encoded := base64.StdEncoding.EncodeToString(value)
	_, stderr, code := runner.Run(ctx, "security",
		"add-generic-password", "-U", "-s", Service, "-a", key, "-w", encoded)
	if code != 0 {
		return fmt.Errorf("security add-generic-password failed (exit %d): %s", code, redactStderr(stderr))
	}
	return nil
}

func (keychainBackend) Delete(ctx context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	_, stderr, code := runner.Run(ctx, "security",
		"delete-generic-password", "-s", Service, "-a", key)
	if code != 0 && !strings.Contains(stderr, "could not be found") {
		return fmt.Errorf("security delete-generic-password failed (exit %d)", code)
	}
	return nil
}

// redactStderr keeps short diagnostics but never echoes payload material.
func redactStderr(stderr string) string {
	s := strings.TrimSpace(stderr)
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
