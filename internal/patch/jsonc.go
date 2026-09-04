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
// Comments are irrelevant to a read, so a standardized clone supplies the same
// decoded traversal used for plain JSON while the original AST stays untouched.
func GetPointerJSONC(doc []byte, pointer string) (json.RawMessage, bool, error) {
	tokens, err := splitPointer(pointer)
	if err != nil {
		return nil, false, err
	}
	_, root, err := parseJSONC(doc)
	if err != nil {
		return nil, false, err
	}
	return getDecodedPointer(root, tokens)
}

// SetPointerJSONC returns the document with the value at pointer replaced or
// created, preserving every comment, trailing comma, and the original
// formatting elsewhere. The pointer's parent object must already exist (a
// single missing leaf member is created). It applies an RFC 6902 "add"
// operation: for an existing object member that replaces it, otherwise it
// creates the member (RFC 6902 §4.1).
func SetPointerJSONC(doc []byte, pointer string, value json.RawMessage) ([]byte, error) {
	tokens, err := splitPointer(pointer)
	if err != nil {
		return nil, err
	}
	parsed, root, err := parseJSONC(doc)
	if err != nil {
		return nil, err
	}
	if _, err := decodeDoc(value); err != nil {
		return nil, fmt.Errorf("pointer value: %w", err)
	}
	operations, _, err := planPointerRewrite(root, tokens, pointer, value, false, false)
	if err != nil {
		return nil, err
	}
	return patchAndPack(parsed, operations, pointer)
}

// DeletePointerJSONC returns the document with the member at pointer removed,
// preserving comments and formatting. A missing member is not an error
// (matching DeletePointer), so an absent pointer returns the document
// unchanged.
func DeletePointerJSONC(doc []byte, pointer string) ([]byte, error) {
	tokens, err := splitPointer(pointer)
	if err != nil {
		return nil, err
	}
	parsed, root, err := parseJSONC(doc)
	if err != nil {
		return nil, err
	}
	operations, changed, err := planPointerRewrite(root, tokens, pointer, nil, true, false)
	if err != nil {
		return nil, err
	}
	if !changed {
		return append([]byte(nil), doc...), nil
	}
	return patchAndPack(parsed, operations, pointer)
}

func parseJSONC(doc []byte) (*hujson.Value, any, error) {
	parsed, err := hujson.Parse(doc)
	if err != nil {
		return nil, nil, fmt.Errorf("parse jsonc: %w", err)
	}
	// Standardize only the clone: decodeDoc then supplies strict semantic
	// validation (including duplicate-member rejection), while parsed retains
	// every comment, trailing comma, and formatting byte needed for a write.
	standard := parsed.Clone()
	standard.Standardize()
	root, err := decodeDoc(standard.Pack())
	if err != nil {
		return nil, nil, fmt.Errorf("parse jsonc: %w", err)
	}
	return &parsed, root, nil
}

// patchAndPack applies RFC 6902 operations to a parsed JSON or JSONC document
// and repacks it, preserving the document's surrounding extra. Adding,
// replacing, or removing a top-level member can reset that extra (leading
// comments live in BeforeExtra), so it is saved and restored around the patch.
func patchAndPack(parsed *hujson.Value, operations []pointerOperation, pointer string) ([]byte, error) {
	ops, err := json.Marshal(operations)
	if err != nil {
		return nil, err
	}
	before, after := parsed.BeforeExtra, parsed.AfterExtra
	if err := parsed.Patch(ops); err != nil {
		return nil, fmt.Errorf("patch json pointer %s: %w", pointer, err)
	}
	parsed.BeforeExtra, parsed.AfterExtra = before, after
	return parsed.Pack(), nil
}
