// Package artifact implements the three auth-artifact primitives
// (json-pointer, file, keychain). It is the single place that reads and
// writes live credential state; adapters only declare specs.
package artifact

import (
	"bytes"
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
	Name   string // stable artifact name, e.g. "oauth_account"
	Kind   string // constants.KindJSONPointer | KindFile | KindKeychain
	Target string // file path, or keychain service name
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
	// KeychainReplace marks a KindKeychain item whose account attribute is a
	// per-login opaque id that varies between accounts (codex keyring's
	// `cli|<opaque>`), unlike claude/cursor whose account is a stable constant.
	// kae captures the live account verbatim into the snapshot (the apply path
	// sets KeychainAccount from it) and, on apply, deletes the existing item
	// before writing the target's, so exactly one item of the service exists
	// afterwards (robust whether the tool matches by service only or
	// service+account). See docs/ADAPTERS.md (Codex keyring) and docs/DATA-MODEL.md.
	KeychainReplace bool
	// KeychainMatchAccount scopes a KindKeychain item to KeychainAccount on
	// both read and write, for a service shared with other tools where only one
	// account is kae's (agy's gemini service: only acct=antigravity is agy's,
	// the rest belong to the Gemini ecosystem). Unlike the default service-only
	// match, kae reads/writes/deletes solely the KeychainAccount item and never
	// reuses or touches a sibling item under a different account. The account is
	// a fixed literal (not captured per-login), so it is incompatible with
	// KeychainReplace. See docs/ADAPTERS.md (agy keyring).
	KeychainMatchAccount bool
	// JSONC marks a KindJSONPointer Target as a JSONC document (standard JSON
	// plus // and /* */ comments and trailing commas, e.g. GitHub Copilot's
	// ~/.copilot/config.json). Reads ignore the comments; writes preserve
	// them and the surrounding formatting, mutating only the pointer value.
	JSONC bool
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
		// Opaque credentials kae handles are single-line raw tokens (Cursor's
		// bare JWT, agy's antigravity token); an interior newline signals a
		// corrupted or wrong payload, so refuse it rather than write it back.
		if bytes.ContainsAny(payload, "\r\n") {
			return fmt.Errorf("%w: keychain item %q payload is not a single line", ErrUnsafe, sp.Target)
		}
		return nil
	}
	if _, ok, err := patch.GetPointer(payload, sp.Pointer); err != nil || !ok {
		return fmt.Errorf("%w: keychain item %q payload is not the expected JSON shape", ErrUnsafe, sp.Target)
	}
	return nil
}

// ReadKeychainAccount returns the live account attribute of a KindKeychain
// spec's item, for capturing a per-login dynamic account (codex keyring's
// `cli|<opaque>`) into the snapshot so apply can recreate the right item. It is
// a separate read so the hot status/Detect path (plain ReadLive) never pays for
// it. Returns "" for a non-keychain spec or an absent item.
func ReadKeychainAccount(ctx context.Context, sp Spec) (string, error) {
	if sp.Kind != constants.KindKeychain {
		return "", nil
	}
	account, _, err := keychain.ItemAccount(ctx, sp.Target)
	return account, err
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
		var raw []byte
		var found bool
		if sp.JSONC {
			raw, found, err = patch.GetPointerJSONC(doc, sp.Pointer)
		} else {
			raw, found, err = patch.GetPointer(doc, sp.Pointer)
		}
		if err != nil {
			return Value{}, fmt.Errorf("%w: %s is not a JSON object (%v)", ErrUnsafe, sp.Target, err)
		}
		if !found {
			return Value{}, nil
		}
		return Value{Data: raw, Present: true}, nil

	case constants.KindKeychain:
		var payload []byte
		var found bool
		var err error
		if sp.KeychainMatchAccount && sp.KeychainAccount != "" {
			payload, found, err = keychain.ReadItemForAccount(ctx, sp.Target, sp.KeychainAccount)
		} else {
			payload, found, err = keychain.ReadItem(ctx, sp.Target)
		}
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
		switch {
		case v.Present && sp.JSONC:
			updated, err = patch.SetPointerJSONC(doc, sp.Pointer, v.Data)
		case v.Present:
			updated, err = patch.SetPointer(doc, sp.Pointer, v.Data)
		case sp.JSONC:
			updated, err = patch.DeletePointerJSONC(doc, sp.Pointer)
		default:
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
		matchAccount := sp.KeychainMatchAccount && sp.KeychainAccount != ""
		if !v.Present {
			// The captured account had no keychain item; applying it removes
			// the live item (mirrors the file/json-pointer absent cases). A
			// match-account spec deletes only its own account's item, never a
			// sibling under a different account of the shared service.
			if matchAccount {
				return keychain.DeleteItemForAccount(ctx, sp.Target, sp.KeychainAccount)
			}
			return keychain.DeleteItem(ctx, sp.Target)
		}
		// Write the captured bytes verbatim (see ReadLive): re-serializing
		// the payload would make the owning tool reject the credential.
		if err := keychainGuard(sp, v.Data); err != nil {
			return err
		}
		if matchAccount {
			// Shared-service item keyed by a fixed account (agy gemini/antigravity):
			// upsert only that account's item (-U matches service+account), so a
			// sibling item under a different account is never read, reused, or
			// overwritten. No replace-delete: that could remove a sibling.
			return keychain.WriteItem(ctx, sp.Target, sp.KeychainAccount, v.Data)
		}
		// A KeychainReplace item (codex keyring) carries its captured opaque
		// account verbatim, so that account wins and the prior item is deleted
		// first to guarantee a single live item. Otherwise (claude/cursor, a
		// stable constant account) the existing item's account is reused.
		account := sp.KeychainAccount
		replace := sp.KeychainReplace && account != ""
		if !replace {
			// Stable-account item (claude/cursor): the existing item's account
			// wins when present, so a re-login that changed it is honored.
			if existing, _, err := keychain.ItemAccount(ctx, sp.Target); err == nil && existing != "" {
				account = existing
			}
		}
		if account == "" {
			account = "kagikae"
		}
		if replace {
			// Per-login dynamic account (codex keyring): delete the prior item
			// so exactly one item of this service exists after writing the
			// target's, under its captured account.
			if err := keychain.DeleteItem(ctx, sp.Target); err != nil {
				return err
			}
		}
		return keychain.WriteItem(ctx, sp.Target, account, v.Data)

	default:
		return fmt.Errorf("unknown artifact kind %q", sp.Kind)
	}
}
