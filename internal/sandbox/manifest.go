// Package sandbox checks and splits compact (schema_version 2) curriculum
// manifests for "antistatic sandbox check" and "push".
//
// Manifests contain sealed answers, so nothing here ever reports a field's
// value: problems are a path (items[b012].curves[0].labels[3]) and a code.
package sandbox

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// Manifest is a decoded manifest file. Numbers are json.Number, so they
// re-encode exactly as written.
type Manifest struct {
	Root   map[string]interface{}
	SHA256 string // of the file's bytes
	Bytes  int
}

// Load reads and decodes a manifest file.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse decodes manifest bytes.
func Parse(data []byte) (*Manifest, error) {
	value, err := Decode(data)
	if err != nil {
		return nil, err
	}
	root, ok := value.(map[string]interface{})
	if !ok {
		return nil, errors.New("the manifest is not a JSON object")
	}
	sum := sha256.Sum256(data)
	return &Manifest{Root: root, SHA256: hex.EncodeToString(sum[:]), Bytes: len(data)}, nil
}

// Decode parses one JSON value with numbers preserved (json.Number). The
// error never quotes the input.
func Decode(data []byte) (interface{}, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var value interface{}
	if err := dec.Decode(&value); err != nil {
		return nil, fmt.Errorf("the manifest is not valid JSON (%s)", jsonErrorWhere(err))
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("the manifest has trailing data after its JSON object")
	}
	return value, nil
}

func jsonErrorWhere(err error) string {
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return fmt.Sprintf("syntax error at byte %d", syntax.Offset)
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return "unexpected end of file"
	}
	return "unreadable"
}

// Canonical encodes v as canonical JSON: object keys sorted, no HTML
// escaping, numbers as decoded, no trailing newline.
func Canonical(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// SHA256Hex is the lowercase hex SHA-256 of b.
func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Source is the manifest's optional source block.
type Source struct {
	Commit string
	Dirty  bool
	// Present is false when the manifest has no source block.
	Present bool
}

// Source reads source.commit and source.dirty.
func (m *Manifest) Source() Source {
	raw, ok := m.Root["source"].(map[string]interface{})
	if !ok {
		return Source{}
	}
	s := Source{Present: true}
	s.Commit, _ = raw["commit"].(string)
	s.Dirty, _ = raw["dirty"].(bool)
	return s
}

func asList(v interface{}) []interface{} {
	list, _ := v.([]interface{})
	return list
}

func asObject(v interface{}) map[string]interface{} {
	obj, _ := v.(map[string]interface{})
	return obj
}

// itemBars is the number of bars an item expands to: the sum of its curves'
// key counts.
func itemBars(item map[string]interface{}) int {
	n := 0
	for _, c := range asList(item["curves"]) {
		n += len(asList(asObject(c)["keys"]))
	}
	return n
}
