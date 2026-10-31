// Package ast is the small slice of github.com/bytedance/sonic/ast that the
// dependency graph needs: locate a path in a JSON document and hand back the
// raw sub-document. Pure Go, encoding/json based, builds on 32-bit targets.
package ast

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrNotExist is returned when the requested path is not in the document.
var ErrNotExist = errors.New("sonic/ast: value not exist")

// Node is a located JSON value (its raw encoding).
type Node struct {
	raw json.RawMessage
}

// Exists reports whether the node holds a value.
func (n Node) Exists() bool { return len(n.raw) > 0 }

// Raw returns the raw JSON text of the node.
func (n Node) Raw() (string, error) {
	if !n.Exists() {
		return "", ErrNotExist
	}
	return string(n.raw), nil
}

// MarshalJSON returns the raw JSON of the node.
func (n Node) MarshalJSON() ([]byte, error) {
	if !n.Exists() {
		return []byte("null"), nil
	}
	return append([]byte(nil), n.raw...), nil
}

// Interface decodes the node into a generic value.
func (n Node) Interface() (interface{}, error) {
	if !n.Exists() {
		return nil, ErrNotExist
	}
	var v interface{}
	if err := json.Unmarshal(n.raw, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// String returns the node's string value; it errors for non-string nodes.
func (n Node) String() (string, error) {
	if !n.Exists() {
		return "", ErrNotExist
	}
	var s string
	if err := json.Unmarshal(n.raw, &s); err != nil {
		return "", err
	}
	return s, nil
}

// GetFromString walks path through src. Integer path elements index arrays,
// string elements select object keys.
func GetFromString(src string, path ...interface{}) (Node, error) {
	cur := json.RawMessage(src)
	if !json.Valid(cur) {
		return Node{}, errors.New("sonic/ast: invalid JSON")
	}
	for _, p := range path {
		switch key := p.(type) {
		case string:
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(cur, &obj); err != nil {
				return Node{}, fmt.Errorf("sonic/ast: %q: not an object: %w", key, err)
			}
			next, ok := obj[key]
			if !ok {
				return Node{}, ErrNotExist
			}
			cur = next
		case int:
			var arr []json.RawMessage
			if err := json.Unmarshal(cur, &arr); err != nil {
				return Node{}, fmt.Errorf("sonic/ast: [%d]: not an array: %w", key, err)
			}
			if key < 0 || key >= len(arr) {
				return Node{}, ErrNotExist
			}
			cur = arr[key]
		default:
			return Node{}, fmt.Errorf("sonic/ast: unsupported path element %T", p)
		}
	}
	return Node{raw: append(json.RawMessage(nil), cur...)}, nil
}
