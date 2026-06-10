package secret

import (
	"context"
	"fmt"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/keychain"
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
		if strings.Contains(stderr, keychain.NotFoundMarker) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("security find-generic-password failed (exit %d)", code)
	}
	value, err := decodePayload(BackendKeychain, key, stdout)
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}

func (keychainBackend) Set(ctx context.Context, key string, value []byte) error {
	if err := validateKey(key); err != nil {
		return err
	}
	_, stderr, code := runner.Run(ctx, "security",
		"add-generic-password", "-U", "-s", Service, "-a", key, "-w", encodePayload(value))
	if code != 0 {
		return fmt.Errorf("security add-generic-password failed (exit %d): %s", code, runner.Snippet(stderr))
	}
	return nil
}

func (keychainBackend) Delete(ctx context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	_, stderr, code := runner.Run(ctx, "security",
		"delete-generic-password", "-s", Service, "-a", key)
	if code != 0 && !strings.Contains(stderr, keychain.NotFoundMarker) {
		return fmt.Errorf("security delete-generic-password failed (exit %d)", code)
	}
	return nil
}
