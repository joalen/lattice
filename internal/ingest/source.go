package ingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// SplitDocuments turns one file's bytes into documents: a top-level object is
// one document, a top-level array is one document per element.
func SplitDocuments(raw []byte) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("empty input")
	}
	switch trimmed[0] {
	case '[':
		var docs []json.RawMessage
		if err := json.Unmarshal(trimmed, &docs); err != nil {
			return nil, err
		}
		return docs, nil
	case '{':
		return []json.RawMessage{append(json.RawMessage(nil), trimmed...)}, nil
	}
	return nil, errors.New("top-level JSON must be an object or an array of objects")
}

// LoadFS walks fsys under root and collects documents from every .json file,
// in lexical order so results are deterministic. Taking an fs.FS instead of a
// directory string means tests can use an in-memory fstest.MapFS.
func LoadFS(fsys fs.FS, root string) ([]json.RawMessage, error) {
	var docs []json.RawMessage

	err := fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".json") {
			return nil
		}
		raw, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		found, err := SplitDocuments(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		docs = append(docs, found...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("no JSON documents found under %q", root)
	}
	return docs, nil
}

// LoadPath accepts either a directory (every .json file inside) or a single file.
func LoadPath(path string) ([]json.RawMessage, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return LoadFS(os.DirFS(path), ".")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	docs, err := SplitDocuments(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return docs, nil
}
