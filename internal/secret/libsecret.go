package secret

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/webkaz-labs/kagikae/internal/runner"
)

// libsecretBackend stores entries via secret-tool (Secret Service / KWallet).
// Attributes: service=kagikae, key=<key>. The payload is passed via stdin on
// store, never argv. secret-tool exits 1 both for "not found" and real
// errors; non-empty stderr is treated as the error discriminator.
type libsecretBackend struct{}

func (libsecretBackend) Name() string { return BackendLibsecret }

func (libsecretBackend) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if err := validateKey(key); err != nil {
		return nil, false, err
	}
	stdout, stderr, code := runner.Run(ctx, "secret-tool",
		"lookup", "service", Service, "key", key)
	if code != 0 {
		if strings.TrimSpace(stderr) == "" {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("secret-tool lookup failed (exit %d): %s", code, redactStderr(stderr))
	}
	value, err := base64.StdEncoding.DecodeString(strings.TrimSpace(stdout))
	if err != nil {
		return nil, false, fmt.Errorf("libsecret entry %s is not kagikae-encoded: %w", key, err)
	}
	return value, true, nil
}

func (libsecretBackend) Set(ctx context.Context, key string, value []byte) error {
	if err := validateKey(key); err != nil {
		return err
	}
	encoded := base64.StdEncoding.EncodeToString(value)
	_, stderr, code := runner.RunInput(ctx, encoded, "secret-tool",
		"store", "--label", Service+"/"+key, "service", Service, "key", key)
	if code != 0 {
		return fmt.Errorf("secret-tool store failed (exit %d): %s", code, redactStderr(stderr))
	}
	return nil
}

func (libsecretBackend) Delete(ctx context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	_, stderr, code := runner.Run(ctx, "secret-tool",
		"clear", "service", Service, "key", key)
	if code != 0 && strings.TrimSpace(stderr) != "" {
		return fmt.Errorf("secret-tool clear failed (exit %d): %s", code, redactStderr(stderr))
	}
	return nil
}
