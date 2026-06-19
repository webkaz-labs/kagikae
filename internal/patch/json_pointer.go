// Package patch implements JSON Pointer reads/writes that preserve every
// other key, plus atomic file writes. It is the only place that mutates
// upstream credential files.
package patch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// decodeDoc parses a JSON document preserving number formatting via
// json.Number so re-encoding cannot corrupt large integers or floats.
func decodeDoc(doc []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}
	return v, nil
}

// EncodeJSON is the single JSON-file encoding policy (2-space indent, no
// HTML escaping, trailing newline). state and backup metadata use it too.
func EncodeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// splitPointer parses an RFC 6901 pointer like "/oauthAccount" into tokens.
func splitPointer(pointer string) ([]string, error) {
	if pointer == "" || !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("invalid json pointer %q", pointer)
	}
	raw := strings.Split(pointer[1:], "/")
	tokens := make([]string, len(raw))
	for i, t := range raw {
		t = strings.ReplaceAll(t, "~1", "/")
		t = strings.ReplaceAll(t, "~0", "~")
		tokens[i] = t
	}
	return tokens, nil
}

// GetPointer returns the raw JSON value at pointer and whether it exists.
func GetPointer(doc []byte, pointer string) (json.RawMessage, bool, error) {
	tokens, err := splitPointer(pointer)
	if err != nil {
		return nil, false, err
	}
	v, err := decodeDoc(doc)
	if err != nil {
		return nil, false, err
	}
	for _, tok := range tokens {
		switch node := v.(type) {
		case map[string]any:
			child, ok := node[tok]
			if !ok {
				return nil, false, nil
			}
			v = child
		case []any:
			idx, err := strconv.Atoi(tok)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, false, nil
			}
			v = node[idx]
		default:
			return nil, false, nil
		}
	}
	raw, err := encodeRaw(v)
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

func encodeRaw(v any) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return json.RawMessage(bytes.TrimRight(buf.Bytes(), "\n")), nil
}

// SetPointer returns the document with the value at pointer replaced (or
// created). All sibling keys are preserved; only single-level-missing object
// paths are created. Array index creation is not supported.
func SetPointer(doc []byte, pointer string, value json.RawMessage) ([]byte, error) {
	return rewritePointer(doc, pointer, value, false)
}

// DeletePointer returns the document with the key at pointer removed. A
// missing key is not an error.
func DeletePointer(doc []byte, pointer string) ([]byte, error) {
	return rewritePointer(doc, pointer, nil, true)
}

func rewritePointer(doc []byte, pointer string, value json.RawMessage, remove bool) ([]byte, error) {
	tokens, err := splitPointer(pointer)
	if err != nil {
		return nil, err
	}
	root, err := decodeDoc(doc)
	if err != nil {
		return nil, err
	}
	node, ok := root.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("document root is not a json object")
	}
	for _, tok := range tokens[:len(tokens)-1] {
		child, exists := node[tok]
		if !exists {
			if remove {
				return EncodeJSON(root) // nothing to remove
			}
			created := map[string]any{}
			node[tok] = created
			node = created
			continue
		}
		childObj, isObj := child.(map[string]any)
		if !isObj {
			return nil, fmt.Errorf("pointer %s traverses a non-object", pointer)
		}
		node = childObj
	}
	leaf := tokens[len(tokens)-1]
	if remove {
		delete(node, leaf)
	} else {
		parsed, err := decodeDoc(value)
		if err != nil {
			return nil, fmt.Errorf("pointer value: %w", err)
		}
		node[leaf] = parsed
	}
	return EncodeJSON(root)
}
