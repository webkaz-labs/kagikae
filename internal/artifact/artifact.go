// Package artifact implements the three auth-artifact primitives
// (json-pointer, file, keychain). It is the single place that reads and
// writes live credential state; adapters only declare specs.
package artifact

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
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
	// KeychainMatchAccount scopes a KindKeychain item to KeychainAccount on read,
	// write and delete, for a service under which more than one legitimate item
	// can live. Two shapes need it:
	//
	//   - a service shared with other tools, where only one account is kae's
	//     (agy's gemini service: only acct=antigravity is agy's, the rest belong
	//     to the Gemini ecosystem);
	//   - a service whose *account* is what scopes the item to one tool home
	//     (codex's `Codex Auth`, one item per CODEX_HOME).
	//
	// Either way kae reads, writes and deletes solely the KeychainAccount item and
	// never reuses or touches a sibling under a different account. The account
	// comes from the adapter — a fixed literal (agy) or derived from the tool's
	// home (codex) — never from the live item, which is a different item's account
	// whenever more than one exists. Treating codex's account as an opaque
	// per-login id and its service as single-item is what made a switch delete
	// another CODEX_HOME's login. See docs/ADAPTERS.md.
	KeychainMatchAccount bool
	// KeychainDirBindable marks a KindKeychain item whose **identity** is derived
	// from the tool's isolation env var, so each bound directory resolves to its own
	// item. An item's identity is service + account, and either half can be what
	// moves: claude's service name does (`Claude Code-credentials-<sha8(configDir)>`)
	// while codex's account does (`cli|<16 hex of sha256(canonical CODEX_HOME)>`
	// under one fixed service). The flag is about the whole identity because that is
	// what its consumer asks — may kae give this directory its own item — and naming
	// it after the service name alone made the two questions look like one.
	//
	// The default of false is the safe one: an undeclared item is left alone and the
	// tool reported as unisolatable, rather than kae writing a *global* login for
	// what the user asked to be per-directory. Declaring it is a claim kae has
	// verified end to end, not an inference from the derivation existing — codex's
	// identity has moved with `CODEX_HOME` all along, and the missing half was never
	// the hash but the item's lifecycle (docs/ROADMAP.md).
	KeychainDirBindable bool
	// JSONC marks a KindJSONPointer Target as a JSONC document (standard JSON
	// plus // and /* */ comments and trailing commas, e.g. GitHub Copilot's
	// ~/.copilot/config.json). Reads ignore the comments; writes preserve
	// them and the surrounding formatting, mutating only the pointer value.
	JSONC bool
	// IdentityOnly marks an artifact that records *who* is logged in without
	// being part of what authenticates (claude's /oauthAccount identity cache).
	// kae switches it so the tool attributes work to the applied account, and
	// every consequence follows from it not being a credential:
	//
	//   - it is not evidence of a login, so its live presence alone does not make
	//     a credential-less state capturable;
	//   - a change to it is not an auth change, so a re-login to the same account
	//     is still reported as unchanged;
	//   - the live copy is not authoritative — the tool may have stopped
	//     maintaining it — so a recapture keeps the value the snapshot recorded;
	//   - losing it is safe and self-correcting, so a snapshot or backup that
	//     lacks it (captured before it existed, or with its payload gone from the
	//     secret store) applies it as absent instead of failing the whole
	//     operation, and the tool rebuilds it.
	//
	// Never set it on a credential: every one of those would be wrong there, and
	// the last would silently log the user out.
	IdentityOnly bool
	// IdentityKeys names the top-level JSON keys of an IdentityOnly payload that
	// actually identify the account. Everything else in the payload is volatile
	// bookkeeping the tool rewrites on its own schedule (claude renews
	// oauthAccount.profileFetchedAt and the plan fields whenever it refetches the
	// profile), so only these keys may be compared when asking "is the live
	// identity still the one kae applied?" — a byte comparison would report a
	// correctly-switched account as drift as soon as the tool touched a timestamp.
	// Empty means "no keyed comparison available": comparers fall back to bytes.
	//
	// This is the identity counterpart of the credential comparison, and only
	// that: a credential is compared byte for byte and must stay that way, since
	// one differing bit there is a different credential.
	IdentityKeys []string
}

// WholeDocument reports whether a spec kind round-trips the artifact's *entire*
// document rather than the value under a JSON pointer. It lives here, next to the
// kinds, because it is a property of what ReadLive and ApplyLive do with each one:
// KindFile and KindKeychain carry the whole document, KindJSONPointer carries only
// the pointer value.
//
// The two shapes are not interchangeable, and callers comparing a stored payload
// against a freshly resolved spec use this to refuse a transition between them —
// applying a whole document through a pointer spec nests it under its own key and
// succeeds, which the owning tool then reads as malformed. A new kind must be
// classified here, or that check silently mis-files it.
func WholeDocument(kind string) bool {
	return kind == constants.KindFile || kind == constants.KindKeychain
}

// Value is one captured artifact value. Present=false records that the
// artifact did not exist live; applying it removes the live artifact.
type Value struct {
	Data    []byte
	Present bool
}

// keychainIdentified refuses a keychain spec that says its item is identified by
// service+account but carries no account. Widening such a spec to a service-only
// match is never safe: the service holds items kae does not own (another codex
// home's login, another tool's `gemini` item), so a read would capture one and a
// delete would destroy one. The case is reachable from a **legacy backup record**
// — codex's old capture wrote `keychain_replace` with no account when no item was
// live — so it is refused at the primitive rather than trusted to callers.
func keychainIdentified(sp Spec) error {
	if sp.KeychainMatchAccount && sp.KeychainAccount == "" {
		return fmt.Errorf("%w: keychain item %q is identified by service and account, "+
			"but this record carries no account; refusing to touch the service as a whole",
			ErrUnsafe, sp.Target)
	}
	return nil
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
		if err := keychainIdentified(sp); err != nil {
			return Value{}, err
		}
		var payload []byte
		var found bool
		var err error
		if sp.KeychainMatchAccount {
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
		// Resolve a symlink before reading, so the read and the write land on the
		// same file. A bond dir links every entry of the real tool home into
		// itself, so a mixed-state target there can be a link back to it
		// (<bond>/.claude.json -> ~/.claude/.claude.json, when claude has written
		// one inside ~/.claude); an atomic rename onto the link would replace it
		// with a private copy and silently end the sharing. Resolving after the
		// read would leave a window where a retargeted link makes kae write file
		// A's content over file B. A link that cannot be resolved is refused,
		// never forked. Whole-file credentials (KindFile) keep replacing their own
		// path, so a credential is never written through a link.
		target := sp.Target
		if resolved, rerr := filepath.EvalSymlinks(target); rerr == nil {
			target = resolved
		} else if info, lerr := os.Lstat(target); lerr == nil && info.Mode()&os.ModeSymlink != 0 {
			// A link that resolves to nothing is logically absent, so removing the
			// pointer is a no-op. Any other resolution failure (a cycle, an
			// unreadable parent directory) says nothing about the target's contents
			// and must not be reported as a successful removal.
			if !v.Present && errors.Is(rerr, fs.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("%w: refusing to touch %s (unresolvable symlink: %v)", ErrUnsafe, target, rerr)
		}
		doc, err := os.ReadFile(target)
		switch {
		case os.IsNotExist(err):
			if !v.Present {
				return nil
			}
			doc = []byte("{}")
		case err != nil:
			return fmt.Errorf("read %s: %w", target, err)
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
			return fmt.Errorf("%w: refusing to rewrite %s (%v)", ErrUnsafe, target, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fmt.Errorf("create dir for %s: %w", target, err)
		}
		return patch.WriteFileAtomic(target, updated, patch.CredentialFileMode)

	case constants.KindKeychain:
		if err := keychainIdentified(sp); err != nil {
			return err
		}
		if !v.Present {
			// The captured account had no keychain item; applying it removes
			// the live item (mirrors the file/json-pointer absent cases). A
			// match-account spec deletes only its own account's item, never a
			// sibling under a different account of the shared service.
			if sp.KeychainMatchAccount {
				return keychain.DeleteItemForAccount(ctx, sp.Target, sp.KeychainAccount)
			}
			return keychain.DeleteItem(ctx, sp.Target)
		}
		// Write the captured bytes verbatim (see ReadLive): re-serializing
		// the payload would make the owning tool reject the credential.
		if err := keychainGuard(sp, v.Data); err != nil {
			return err
		}
		if sp.KeychainMatchAccount {
			// Item keyed by service+account (agy's gemini/antigravity, codex's
			// per-CODEX_HOME `Codex Auth`): upsert only that account's item (-U
			// matches service+account), so a sibling item under a different account
			// is never read, reused, or overwritten. No delete-before-write — that
			// is what removed another codex home's login.
			return keychain.WriteItem(ctx, sp.Target, sp.KeychainAccount, v.Data)
		}
		// No spec kae ships reaches here any more — every adapter's keychain item is
		// identified by service **and** account. What still does is a rollback of a
		// backup written before the record carried the account, where the live item
		// is the only evidence of what the tool reads.
		//
		// The adapter's account is authoritative wherever there is one; this used to
		// have that backwards, and AGENTS.md's keychain-identity boundary carries why.
		account := sp.KeychainAccount
		if account == "" {
			if existing, _, err := keychain.ItemAccount(ctx, sp.Target); err == nil && existing != "" {
				account = existing
			}
		}
		if account == "" {
			account = "kagikae"
		}
		return keychain.WriteItem(ctx, sp.Target, account, v.Data)

	default:
		return fmt.Errorf("unknown artifact kind %q", sp.Kind)
	}
}
