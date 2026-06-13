package patch

import (
	"encoding/json"
	"fmt"

	"github.com/tailscale/hujson"
)

// JSONC reads and writes documents that are standard JSON plus // and /* */
// comments and trailing commas (JWCC). It is used for upstream config files
// that carry comments kae must not destroy on a pointer patch — e.g. GitHub
// Copilot's ~/.copilot/config.json. Reads ignore comments; writes preserve
// them, the trailing commas, and the surrounding formatting verbatim, mutating
// only the targeted value.

// GetPointerJSONC returns the raw JSON value at pointer and whether it exists.
// Comments are irrelevant to a read, so the document is standardized first and
// the value is extracted with the plain GetPointer.
func GetPointerJSONC(doc []byte, pointer string) (json.RawMessage, bool, error) {
	// Standardize rewrites its argument in place (it blanks comments to keep
	// byte offsets), so pass a copy to leave the caller's slice intact.
	std, err := hujson.Standardize(append([]byte(nil), doc...))
	if err != nil {
		return nil, false, fmt.Errorf("parse jsonc: %w", err)
	}
	return GetPointer(std, pointer)
}

// SetPointerJSONC returns the document with the value at pointer replaced or
// created, preserving every comment, trailing comma, and the original
// formatting elsewhere. The pointer's parent object must already exist (a
// single missing leaf member is created). It applies an RFC 6902 "add"
// operation: for an existing object member that replaces it, otherwise it
// creates the member (RFC 6902 §4.1).
func SetPointerJSONC(doc []byte, pointer string, value json.RawMessage) ([]byte, error) {
	return patchAndPack(doc, map[string]any{"op": "add", "path": pointer, "value": value}, pointer)
}

// DeletePointerJSONC returns the document with the member at pointer removed,
// preserving comments and formatting. A missing member is not an error
// (matching DeletePointer), so an absent pointer returns the document
// unchanged.
func DeletePointerJSONC(doc []byte, pointer string) ([]byte, error) {
	v, err := hujson.Parse(doc)
	if err != nil {
		return nil, fmt.Errorf("parse jsonc: %w", err)
	}
	if v.Find(pointer) == nil {
		return doc, nil
	}
	return patchAndPack(doc, map[string]any{"op": "remove", "path": pointer}, pointer)
}

// patchAndPack applies a single RFC 6902 operation to a JSONC document and
// repacks it, preserving the document's surrounding extra. Adding, replacing,
// or removing a top-level member can reset that extra (the leading // comments
// live in BeforeExtra), so it is saved and restored around the patch.
func patchAndPack(doc []byte, op map[string]any, pointer string) ([]byte, error) {
	v, err := hujson.Parse(doc)
	if err != nil {
		return nil, fmt.Errorf("parse jsonc: %w", err)
	}
	ops, err := json.Marshal([]map[string]any{op})
	if err != nil {
		return nil, err
	}
	before, after := v.BeforeExtra, v.AfterExtra
	if err := v.Patch(ops); err != nil {
		return nil, fmt.Errorf("patch jsonc pointer %s: %w", pointer, err)
	}
	v.BeforeExtra, v.AfterExtra = before, after
	return v.Pack(), nil
}
