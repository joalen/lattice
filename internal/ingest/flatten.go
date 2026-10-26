package ingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ScalarKind is the coarse type of a leaf value.
type ScalarKind string

const (
	KindString ScalarKind = "string"
	KindNumber ScalarKind = "number"
	KindBool   ScalarKind = "bool"
)

// Scalar is one leaf value. Numbers keep their original text (see Flatten).
type Scalar struct {
	Kind ScalarKind
	Text string
}

// Field is everything found at one dot-path in one document.
type Field struct {
	Values []Scalar
	Array  bool // true if the values came from an array of primitives
}

// Flatten turns one JSON object into dot-path -> values, e.g.
//
//	{"address": {"city": "X"}, "tags": ["a", "b"]}
//	  => "address.city": ["X"], "tags": ["a", "b"] (Array)
func Flatten(raw []byte) (map[string]Field, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var root any
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("flatten: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("flatten: trailing data after JSON document")
	}

	obj, ok := root.(map[string]any)
	if !ok {
		return nil, errors.New("flatten: document must be a JSON object")
	}

	out := make(map[string]Field)
	walk("", obj, out)
	return out, nil
}

func walk(path string, node any, out map[string]Field) {
	switch v := node.(type) {
	case map[string]any:
		for k, child := range v {
			walk(joinPath(path, k), child, out)
		}

	case []any:
		scalars := make([]Scalar, 0, len(v))
		for _, item := range v {
			switch item.(type) {
			case nil:
				continue
			case map[string]any, []any:
				return // contains structure: leave the whole array opaque
			}
			if s, ok := scalarOf(item); ok {
				scalars = append(scalars, s)
			}
		}
		if len(scalars) > 0 {
			out[path] = Field{Values: scalars, Array: true}
		}

	default:
		if s, ok := scalarOf(node); ok {
			out[path] = Field{Values: []Scalar{s}}
		}
	}
}

func scalarOf(v any) (Scalar, bool) {
	switch x := v.(type) {
	case string:
		return Scalar{KindString, x}, true
	case json.Number:
		return Scalar{KindNumber, x.String()}, true
	case bool:
		if x {
			return Scalar{KindBool, "true"}, true
		}
		return Scalar{KindBool, "false"}, true
	}
	return Scalar{}, false
}

func joinPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}
