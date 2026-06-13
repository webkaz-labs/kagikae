// Package artifact implements the three auth-artifact primitives
// (json-pointer, file, keychain). It is the single place that reads and
// writes live credential state; adapters only declare specs.
package artifact

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/webkaz-labs/kagikae/internal/constants"
	"github.com/webkaz-labs/kagikae/internal/keychain"
	"github.com/webkaz-labs/kagikae/internal/patch"
)

// ErrUnsafe means the live state failed a structure guard; callers refuse
// the write (exit code 10) instead of best-effort writing.
var ErrUnsafe = errors.New("unsafe operation refused")

// Spec declares one auth artifact of a tool.
type Spec struct {
	Name    string // stable artifact name, e.g. "oauth_account"
	Kind    string // constants.KindJSONPointer | KindFile | KindKeychain
	Target  string // file path, or keychain service name
	// Pointer is a JSON pointer. For KindJSONPointer it selects the
	// sub-value to capture and apply. For KindKeychain it is only a
	// structure guard: the item's bytes are captured and restored verbatim
	// (the owning tool rejects a re-serialized payload), and the pointer
	// just asserts the expected shape is present. An empty pointer on a
	// KindKeychain spec marks an opaque payload — a raw token that is not
	// JSON (Cursor stores a bare JWT); the bytes still round-trip verbatim
	// and the only guard is that they are non-empty.
	Pointer string
	// KeychainAccount is the account attribute used when the keychain item
	// must be created from scratch (normally the existing item's account is
	// reused). Every KindKeychain spec must set it, or new items fall back
	// to the literal account "kagikae".
	KeychainAccount string
}

// Value is one captured artifact value. Present=false records that the
// artifact did not exist live; applying it removes the live artifact.
type Value struct {
	Data    []byte
	Present bool
}

// keychainGuard verifies a captured keychain payload before it is stored or
// applied. The item's bytes always round-trip verbatim (the owning tool
// rejects a re-serialized payload), so the guard never mutates them; it only
// refuses an unrecognized shape. With a JSON pointer the payload must be a
// JSON object containing that pointer. With an empty pointer the payload is
// opaque — a raw token that is not JSON (Cursor stores a bare JWT) — and the
// only check is that it is non-empty.
func keychainGuard(sp Spec, payload []byte) error {
	if sp.Pointer == "" {
		if len(payload) == 0 {
			return fmt.Errorf("%w: keychain item %q payload is empty", ErrUnsafe, sp.Target)
		}
		return nil
	}
	if _, ok, err := patch.GetPointer(payload, sp.Pointer); err != nil || !ok {
		return fmt.Errorf("%w: keychain item %q payload is not the expected JSON shape", ErrUnsafe, sp.Target)
	}
	return nil
}

// ReadLive captures the current live value of the artifact.
func ReadLive(ctx context.Context, sp Spec) (Value, error) {
	switch sp.Kind {
	case constants.KindFile:
		data, err := os.ReadFile(sp.Target)
		if os.IsNotExist(err) {
			return Value{}, nil
		}
		if err != nil {
			return Value{}, fmt.Errorf("read %s: %w", sp.Target, err)
		}
		return Value{Data: data, Present: true}, nil

	case constants.KindJSONPointer:
		doc, err := os.ReadFile(sp.Target)
		if os.IsNotExist(err) {
			return Value{}, nil
		}
		if err != nil {
			return Value{}, fmt.Errorf("read %s: %w", sp.Target, err)
		}
		raw, found, err := patch.GetPointer(doc, sp.Pointer)
		if err != nil {
			return Value{}, fmt.Errorf("%w: %s is not a JSON object (%v)", ErrUnsafe, sp.Target, err)
		}
		if !found {
			return Value{}, nil
		}
		return Value{Data: raw, Present: true}, nil

	case constants.KindKeychain:
		payload, found, err := keychain.ReadItem(ctx, sp.Target)
		if err != nil {
			return Value{}, err
		}
		if !found {
			return Value{}, nil
		}
		// Store the item's bytes verbatim. The owning tool writes its own
		// encoding (Claude Code: compact JSON) and rejects a re-serialized
		// payload, so the guard never extracts and re-encodes a sub-value.
		if err := keychainGuard(sp, payload); err != nil {
			return Value{}, err
		}
		return Value{Data: payload, Present: true}, nil

	default:
		return Value{}, fmt.Errorf("unknown artifact kind %q", sp.Kind)
	}
}

// ApplyLive writes (or removes) the artifact value in the live state.
func ApplyLive(ctx context.Context, sp Spec, v Value) error {
	switch sp.Kind {
	case constants.KindFile:
		if !v.Present {
			if err := os.Remove(sp.Target); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove %s: %w", sp.Target, err)
			}
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(sp.Target), 0o700); err != nil {
			return fmt.Errorf("create dir for %s: %w", sp.Target, err)
		}
		return patch.WriteFileAtomic(sp.Target, v.Data, patch.CredentialFileMode)

	case constants.KindJSONPointer:
		doc, err := os.ReadFile(sp.Target)
		switch {
		case os.IsNotExist(err):
			if !v.Present {
				return nil
			}
			doc = []byte("{}")
		case err != nil:
			return fmt.Errorf("read %s: %w", sp.Target, err)
		}
		var updated []byte
		if v.Present {
			updated, err = patch.SetPointer(doc, sp.Pointer, v.Data)
		} else {
			updated, err = patch.DeletePointer(doc, sp.Pointer)
		}
		if err != nil {
			return fmt.Errorf("%w: refusing to rewrite %s (%v)", ErrUnsafe, sp.Target, err)
		}
		if err := os.MkdirAll(filepath.Dir(sp.Target), 0o700); err != nil {
			return fmt.Errorf("create dir for %s: %w", sp.Target, err)
		}
		return patch.WriteFileAtomic(sp.Target, updated, patch.CredentialFileMode)

	case constants.KindKeychain:
		if !v.Present {
			// The captured account had no keychain item; applying it removes
			// the live item (mirrors the file/json-pointer absent cases).
			return keychain.DeleteItem(ctx, sp.Target)
		}
		// Write the captured bytes verbatim (see ReadLive): re-serializing
		// the payload would make the owning tool reject the credential.
		if err := keychainGuard(sp, v.Data); err != nil {
			return err
		}
		account := sp.KeychainAccount
		if existing, _, err := keychain.ItemAccount(ctx, sp.Target); err == nil && existing != "" {
			account = existing
		}
		if account == "" {
			account = "kagikae"
		}
		return keychain.WriteItem(ctx, sp.Target, account, v.Data)

	default:
		return fmt.Errorf("unknown artifact kind %q", sp.Kind)
	}
}
