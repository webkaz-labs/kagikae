// Package patch implements JSON Pointer reads/writes that preserve every
// other key, plus atomic file writes. It is the only place that mutates
// upstream credential files.
package patch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/tailscale/hujson"
)

// decodeDoc parses exactly one strict JSON value, rejects duplicate object
// members at every nesting level, and preserves number formatting via
// json.Number so re-encoding cannot corrupt large integers or floats.
func decodeDoc(doc []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("unexpected value after top-level value")
		}
		return nil, fmt.Errorf("parse json: %w", err)
	}
	return v, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, isDelim := tok.(json.Delim)
	if !isDelim {
		return tok, nil
	}
	switch delim {
	case '{':
		object := make(map[string]any)
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("object member name is not a string")
			}
			if _, exists := object[key]; exists {
				return nil, fmt.Errorf("duplicate object member %q", key)
			}
			value, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if closeToken, err := dec.Token(); err != nil {
			return nil, err
		} else if closeToken != json.Delim('}') {
			return nil, fmt.Errorf("object closed by %q", closeToken)
		}
		return object, nil
	case '[':
		array := make([]any, 0)
		for dec.More() {
			value, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		if closeToken, err := dec.Token(); err != nil {
			return nil, err
		} else if closeToken != json.Delim(']') {
			return nil, fmt.Errorf("array closed by %q", closeToken)
		}
		return array, nil
	default:
		return nil, fmt.Errorf("unexpected delimiter %q", delim)
	}
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
		var decoded strings.Builder
		for j := 0; j < len(t); j++ {
			if t[j] != '~' {
				decoded.WriteByte(t[j])
				continue
			}
			if j+1 >= len(t) || (t[j+1] != '0' && t[j+1] != '1') {
				return nil, fmt.Errorf("invalid json pointer escape in %q", pointer)
			}
			if t[j+1] == '0' {
				decoded.WriteByte('~')
			} else {
				decoded.WriteByte('/')
			}
			j++
		}
		tokens[i] = decoded.String()
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
	return getDecodedPointer(v, tokens)
}

func getDecodedPointer(v any, tokens []string) (json.RawMessage, bool, error) {
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
// created). All bytes outside the patched member are preserved; missing object
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
	if !remove {
		if _, err := decodeDoc(value); err != nil {
			return nil, fmt.Errorf("pointer value: %w", err)
		}
	}
	operations, changed, err := planPointerRewrite(root, tokens, pointer, value, remove, true)
	if err != nil {
		return nil, err
	}
	if !changed {
		return append([]byte(nil), doc...), nil
	}
	parsed, err := hujson.Parse(doc)
	if err != nil {
		return nil, fmt.Errorf("parse json: %w", err)
	}
	return patchAndPack(&parsed, operations, pointer)
}

type pointerOperation struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value,omitempty"`
}

func addPointerOperation(path string, value json.RawMessage) pointerOperation {
	return pointerOperation{Op: "add", Path: path, Value: value}
}

func removePointerOperation(path string) pointerOperation {
	return pointerOperation{Op: "remove", Path: path}
}

func planPointerRewrite(
	root any,
	tokens []string,
	pointer string,
	value json.RawMessage,
	remove bool,
	createParents bool,
) ([]pointerOperation, bool, error) {
	node, ok := root.(map[string]any)
	if !ok {
		return nil, false, fmt.Errorf("document root is not a json object")
	}
	operations := make([]pointerOperation, 0, len(tokens))
	for i, tok := range tokens[:len(tokens)-1] {
		child, exists := node[tok]
		if !exists {
			if remove {
				return nil, false, nil
			}
			if !createParents {
				return nil, false, fmt.Errorf("pointer %s parent does not exist", pointer)
			}
			created := map[string]any{}
			operations = append(operations,
				addPointerOperation(joinPointer(tokens[:i+1]), json.RawMessage(`{}`)))
			node[tok] = created
			node = created
			continue
		}
		childObj, isObj := child.(map[string]any)
		if !isObj {
			return nil, false, fmt.Errorf("pointer %s traverses a non-object", pointer)
		}
		node = childObj
	}
	leaf := tokens[len(tokens)-1]
	if remove {
		if _, exists := node[leaf]; !exists {
			return nil, false, nil
		}
		operations = append(operations, removePointerOperation(pointer))
	} else {
		operations = append(operations, addPointerOperation(pointer, value))
	}
	return operations, true, nil
}

func joinPointer(tokens []string) string {
	escaped := make([]string, len(tokens))
	for i, token := range tokens {
		token = strings.ReplaceAll(token, "~", "~0")
		escaped[i] = strings.ReplaceAll(token, "/", "~1")
	}
	return "/" + strings.Join(escaped, "/")
}
