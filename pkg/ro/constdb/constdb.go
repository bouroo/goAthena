// Package constdb loads rAthena's db/const.yml (Header CONSTANT_DB) into a
// name→value registry used by the script engine and other lookup consumers
// (item IDs, job IDs, status constants).
package constdb

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Entry is a single constant from rAthena's const.yml.
type Entry struct {
	Name      string
	Value     int64
	Parameter bool
}

// header mirrors rAthena's Header block.
type header struct {
	Type    string `yaml:"Type"`
	Version int    `yaml:"Version"`
}

// Registry holds the merged set of constants loaded from one or more
// const.yml streams.
type Registry struct {
	entries map[string]Entry
}

// NewRegistry returns an empty Registry. Use Load or LoadFile to populate it.
func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]Entry)}
}

// Load parses a single const.yml YAML stream from rd and merges its entries
// into the Registry. Footer.Imports paths inside the stream are NOT followed
// by Load; use LoadFile, which carries the directory needed to resolve them.
//
// A duplicate Name+Value pair is idempotent (no error). A duplicate Name with
// a different Value returns an error wrapping both observed values.
func (r *Registry) Load(rd io.Reader) error {
	if r.entries == nil {
		r.entries = make(map[string]Entry)
	}
	data, err := io.ReadAll(rd)
	if err != nil {
		return fmt.Errorf("read const_db stream: %w", err)
	}
	return r.loadBytes(data, "stream")
}

// LoadFile opens path, parses the YAML, then recursively loads each path
// listed under Footer.Imports resolved relative to path's directory. A
// non-existent import target is skipped silently (matching rAthena's
// import-tmpl workflow); other I/O or parse errors are wrapped.
func (r *Registry) LoadFile(path string) error {
	clean := filepath.Clean(path)
	f, err := os.Open(clean) // #nosec G304 -- path is operator-configured const.yml, not user input
	if err != nil {
		return fmt.Errorf("open const_db %q: %w", clean, err)
	}
	defer func() { _ = f.Close() }()

	data, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("read const_db %q: %w", clean, err)
	}
	if err := r.loadBytes(data, clean); err != nil {
		return err
	}

	imports, err := parseImports(data)
	if err != nil {
		return fmt.Errorf("parse const_db footer %q: %w", clean, err)
	}
	base := filepath.Dir(clean)
	for _, ref := range imports {
		target := resolveImportPath(base, ref)
		if err := r.loadFileRecursive(target); err != nil {
			return err
		}
	}
	return nil
}

// loadBytes parses Header+Body and merges. source is included in error
// messages so callers can tell which file produced the error.
func (r *Registry) loadBytes(data []byte, source string) error {
	if r.entries == nil {
		r.entries = make(map[string]Entry)
	}
	head, body, err := parseDoc(data)
	if err != nil {
		return fmt.Errorf("parse const_db %s: %w", source, err)
	}
	if err := validateHeader(head.Type, head.Version); err != nil {
		return fmt.Errorf("const_db %s: %w", source, err)
	}
	return r.mergeBody(body, source)
}

func (r *Registry) loadFileRecursive(path string) error {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat const_db import %q: %w", path, err)
	}
	if err := r.LoadFile(path); err != nil {
		return fmt.Errorf("load const_db import %q: %w", path, err)
	}
	return nil
}

// LoadFile is a convenience that builds a fresh Registry and loads path,
// following Footer.Imports recursively relative to path's directory.
func LoadFile(path string) (*Registry, error) {
	reg := NewRegistry()
	if err := reg.LoadFile(path); err != nil {
		return nil, err
	}
	return reg, nil
}

// Lookup returns the Entry for name. The second return value is false if
// the constant is not registered. The match is case-sensitive and exact.
func (r *Registry) Lookup(name string) (Entry, bool) {
	if r == nil {
		return Entry{}, false
	}
	e, ok := r.entries[name]
	return e, ok
}

// Value returns the int64 value for name. The second return value is false
// if the constant is not registered. Useful when callers do not need the
// Parameter flag.
func (r *Registry) Value(name string) (int64, bool) {
	if r == nil {
		return 0, false
	}
	e, ok := r.entries[name]
	return e.Value, ok
}

// Size returns the number of distinct constants in the registry.
func (r *Registry) Size() int {
	if r == nil {
		return 0
	}
	return len(r.entries)
}

// parseDoc decodes data into a yaml.Node tree, then extracts Header.Type,
// Header.Version, and the raw Body mapping nodes. Using yaml.Node directly
// (rather than decoding into typed structs) lets parseBodyRow report
// per-entry errors with the offending constant name in context.
func parseDoc(data []byte) (header, []*yaml.Node, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return header{}, nil, fmt.Errorf("parse document: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return header{}, nil, fmt.Errorf("expected document node, got kind %d", root.Kind)
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return header{}, nil, fmt.Errorf("expected top-level mapping, got kind %d", doc.Kind)
	}
	var head header
	var body []*yaml.Node
	for i := 0; i+1 < len(doc.Content); i += 2 {
		key := doc.Content[i].Value
		val := doc.Content[i+1]
		switch key {
		case "Header":
			if val.Kind != yaml.MappingNode {
				return header{}, nil, fmt.Errorf("header mapping: expected mapping node, got kind %d", val.Kind)
			}
			if err := val.Decode(&head); err != nil {
				return header{}, nil, fmt.Errorf("decode header: %w", err)
			}
		case "Body":
			if val.Kind != yaml.SequenceNode {
				return header{}, nil, fmt.Errorf("body sequence: expected sequence node, got kind %d", val.Kind)
			}
			body = val.Content
		}
	}
	return head, body, nil
}

func parseImports(data []byte) ([]string, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse imports: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil, fmt.Errorf("expected document node, got kind %d", root.Kind)
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected top-level mapping, got kind %d", doc.Kind)
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value != "Footer" {
			continue
		}
		footer := doc.Content[i+1]
		if footer.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("footer mapping: expected mapping node, got kind %d", footer.Kind)
		}
		for j := 0; j+1 < len(footer.Content); j += 2 {
			if footer.Content[j].Value != "Imports" {
				continue
			}
			imp := footer.Content[j+1]
			if imp.Kind != yaml.SequenceNode {
				return nil, nil
			}
			out := make([]string, 0, len(imp.Content))
			for _, item := range imp.Content {
				if item.Kind != yaml.MappingNode {
					continue
				}
				for k := 0; k+1 < len(item.Content); k += 2 {
					if item.Content[k].Value == "Path" {
						out = append(out, item.Content[k+1].Value)
						break
					}
				}
			}
			return out, nil
		}
	}
	return nil, nil
}

func validateHeader(headerType string, version int) error {
	switch headerType {
	case "CONSTANT_DB":
		if version != 1 {
			return fmt.Errorf("unsupported Header.Version %d (want 1)", version)
		}
		return nil
	default:
		return fmt.Errorf("unexpected Header.Type %q (want %q)", headerType, "CONSTANT_DB")
	}
}

func (r *Registry) mergeBody(rows []*yaml.Node, source string) error {
	for i, row := range rows {
		entry, err := parseBodyRow(row)
		if err != nil {
			return fmt.Errorf("const_db %s: body index %d: %w", source, i, err)
		}
		if entry.Name == "" {
			return fmt.Errorf("const_db %s: body index %d: %w", source, i, errEmptyName)
		}
		if existing, ok := r.entries[entry.Name]; ok {
			if existing.Value == entry.Value {
				if entry.Parameter && !existing.Parameter {
					existing.Parameter = true
					r.entries[entry.Name] = existing
				}
				continue
			}
			return fmt.Errorf(
				"const_db %s: %w (have %d, want %d)",
				source, errDuplicate(entry.Name), existing.Value, entry.Value,
			)
		}
		r.entries[entry.Name] = entry
	}
	return nil
}

// errDuplicate names the offending constant so callers can match with
// errors.Is without parsing the message.
type errDuplicate string

func (e errDuplicate) Error() string { return fmt.Sprintf("duplicate constant %q", string(e)) }
func (e errDuplicate) Is(target error) bool {
	_, ok := target.(errDuplicate)
	return ok
}

var errEmptyName = errors.New("constant entry with empty name")

type errInvalidValue struct {
	name string
	raw  string
	err  error
}

func (e *errInvalidValue) Error() string {
	if e.name == "" {
		return fmt.Sprintf("invalid value %q: %v", e.raw, e.err)
	}
	return fmt.Sprintf("constant %q has invalid value %q: %v", e.name, e.raw, e.err)
}

func (e *errInvalidValue) Unwrap() error { return e.err }
func (e *errInvalidValue) Is(target error) bool {
	_, ok := target.(*errInvalidValue)
	return ok
}

// parseBodyRow extracts Name, Value, Parameter from a Body mapping node.
// Value is parsed manually so a non-integer scalar produces an
// *errInvalidValue carrying the constant name.
func parseBodyRow(row *yaml.Node) (Entry, error) {
	if row.Kind != yaml.MappingNode {
		return Entry{}, fmt.Errorf("body mapping: expected mapping node, got kind %d", row.Kind)
	}
	var entry Entry
	for i := 0; i+1 < len(row.Content); i += 2 {
		keyNode := row.Content[i]
		valNode := row.Content[i+1]
		switch keyNode.Value {
		case "Name":
			if valNode.Kind != yaml.ScalarNode {
				return entry, fmt.Errorf("name: expected scalar, got kind %d", valNode.Kind)
			}
			entry.Name = valNode.Value
		case "Value":
			v, err := decodeInt64Value(valNode)
			if err != nil {
				return entry, &errInvalidValue{name: entry.Name, raw: valNode.Value, err: err}
			}
			entry.Value = v
		case "Parameter":
			if valNode.Kind != yaml.ScalarNode {
				return entry, fmt.Errorf("parameter: expected scalar, got kind %d", valNode.Kind)
			}
			b, perr := strconv.ParseBool(valNode.Value)
			if perr != nil {
				return entry, fmt.Errorf("parse parameter: %w", perr)
			}
			entry.Parameter = b
		}
	}
	return entry, nil
}

func decodeInt64Value(node *yaml.Node) (int64, error) {
	if node.Kind != yaml.ScalarNode {
		return 0, fmt.Errorf("value: expected scalar, got kind %d", node.Kind)
	}
	if node.Tag == "!!float" {
		return 0, fmt.Errorf("expected int, got float %q", node.Value)
	}
	v, err := strconv.ParseInt(node.Value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse int %q: %w", node.Value, err)
	}
	return v, nil
}

func resolveImportPath(base, ref string) string {
	if filepath.IsAbs(ref) {
		return filepath.Clean(ref)
	}
	return filepath.Clean(filepath.Join(base, ref))
}
