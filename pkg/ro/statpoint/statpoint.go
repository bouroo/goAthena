// Package statpoint loads rAthena's db/(pre-re/)statpoint.yml (Header
// STATPOINT_DB) into a level→points registry that the identity/stats feature
// uses to determine how many stat points a character has at base level N.
package statpoint

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Entry is a single (Level, Points) row from a STATPOINT_DB Body.
type Entry struct {
	Level  uint32
	Points int32
}

// Registry is a lookup table mapping base levels to the cumulative stat
// points granted by the character progression rules.
//
// A nil *Registry is a valid empty registry; every method is nil-safe so
// callers can defer construction until first use.
type Registry struct {
	points map[uint32]int32
}

// NewRegistry returns an empty Registry. Use Load or LoadFile to populate it.
func NewRegistry() *Registry {
	return &Registry{points: make(map[uint32]int32)}
}

// Load parses a single statpoint.yml YAML stream from rd and merges its
// entries into the Registry. Footer.Imports paths inside the stream are NOT
// followed by Load; use LoadFile, which carries the directory needed to
// resolve them.
//
// A duplicate (Level, Points) pair is idempotent (no error). A duplicate
// Level with a different Points value returns an error wrapping the
// errDuplicateLevel sentinel.
func (r *Registry) Load(rd io.Reader) error {
	if r == nil {
		return errors.New("statpoint: nil receiver")
	}
	data, err := io.ReadAll(rd)
	if err != nil {
		return fmt.Errorf("read statpoint_db stream: %w", err)
	}
	return r.loadBytes(data, "stream")
}

// LoadFile opens path, parses the YAML, then recursively loads each path
// listed under Footer.Imports resolved relative to path's directory. A
// non-existent import target is skipped silently (matching rAthena's
// import-tmpl workflow); other I/O or parse errors are wrapped.
func (r *Registry) LoadFile(path string) error {
	if r == nil {
		return errors.New("statpoint: nil receiver")
	}
	return r.loadFile(path, map[string]bool{})
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

// Points returns the cumulative stat points granted at the given base level.
// The second return value is false if the level is not registered.
func (r *Registry) Points(level uint32) (int32, bool) {
	if r == nil {
		return 0, false
	}
	v, ok := r.points[level]
	return v, ok
}

// MaxLevel returns the highest defined base level in the registry. Returns 0
// when the registry is empty or nil.
func (r *Registry) MaxLevel() uint32 {
	if r == nil {
		return 0
	}
	var highest uint32
	for lvl := range r.points {
		if lvl > highest {
			highest = lvl
		}
	}
	return highest
}

// Size returns the number of distinct levels defined in the registry.
func (r *Registry) Size() int {
	if r == nil {
		return 0
	}
	return len(r.points)
}

// Levels returns a freshly allocated, sorted-ascending slice of every
// defined level. The result is a defensive copy; callers may mutate it
// without affecting the registry.
func (r *Registry) Levels() []uint32 {
	if r == nil || len(r.points) == 0 {
		return nil
	}
	out := make([]uint32, 0, len(r.points))
	for lvl := range r.points {
		out = append(out, lvl)
	}
	sortUint32Asc(out)
	return out
}

func sortUint32Asc(s []uint32) {
	for i := 1; i < len(s); i++ {
		v := s[i]
		j := i - 1
		for j >= 0 && s[j] > v {
			s[j+1] = s[j]
			j--
		}
		s[j+1] = v
	}
}

// loadFile reads a single statpoint.yml, merges its Body, and recurses into
// Footer.Imports. visited tracks files already loaded on this code path so a
// cycle (A imports B imports A, or a file importing itself) is rejected with
// a clear error instead of looping forever.
func (r *Registry) loadFile(path string, visited map[string]bool) error {
	clean := filepath.Clean(path)
	if visited[clean] {
		return fmt.Errorf("circular statpoint_db import detected: %q", clean)
	}
	visited[clean] = true

	f, err := os.Open(clean) // #nosec G304 -- path is operator-configured statpoint.yml, not user input
	if err != nil {
		return fmt.Errorf("open statpoint_db %q: %w", clean, err)
	}
	defer func() { _ = f.Close() }()

	data, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("read statpoint_db %q: %w", clean, err)
	}
	if err := r.loadBytes(data, clean); err != nil {
		return err
	}

	imports, err := parseImports(data)
	if err != nil {
		return fmt.Errorf("parse statpoint_db footer %q: %w", clean, err)
	}
	base := filepath.Dir(clean)
	for _, ref := range imports {
		target := resolveImportPath(base, ref)
		if err := r.loadFileRecursive(target, visited); err != nil {
			return err
		}
	}
	return nil
}

// loadBytes parses Header+Body and merges. source is included in error
// messages so callers can tell which file produced the error. An empty
// data slice is treated as a no-op (an import may legitimately resolve to
// an empty file).
func (r *Registry) loadBytes(data []byte, source string) error {
	if r.points == nil {
		r.points = make(map[uint32]int32)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	head, body, err := parseDoc(data)
	if err != nil {
		return fmt.Errorf("parse statpoint_db %s: %w", source, err)
	}
	if head.Type == "" && len(body) == 0 {
		// Empty document body: nothing to validate or merge.
		return nil
	}
	if err := validateHeader(head.Type, head.Version); err != nil {
		return fmt.Errorf("statpoint_db %s: %w", source, err)
	}
	return r.mergeBody(body, source)
}

func (r *Registry) loadFileRecursive(path string, visited map[string]bool) error {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat statpoint_db import %q: %w", path, err)
	}
	if err := r.loadFile(path, visited); err != nil {
		return fmt.Errorf("load statpoint_db import %q: %w", path, err)
	}
	return nil
}

func (r *Registry) mergeBody(rows []*yaml.Node, source string) error {
	for i, row := range rows {
		entry, err := parseBodyRow(row)
		if err != nil {
			return fmt.Errorf("statpoint_db %s: body index %d: %w", source, i, err)
		}
		if entry.Level == 0 {
			return fmt.Errorf("statpoint_db %s: body index %d: %w", source, i, errEmptyLevel)
		}
		if existing, ok := r.points[entry.Level]; ok {
			if existing == entry.Points {
				continue
			}
			return fmt.Errorf(
				"statpoint_db %s: body index %d: %w (have %d, want %d)",
				source, i, errDuplicateLevel(entry.Level), existing, entry.Points,
			)
		}
		r.points[entry.Level] = entry.Points
	}
	return nil
}

type header struct {
	Type    string `yaml:"Type"`
	Version int    `yaml:"Version"`
}

func validateHeader(headerType string, version int) error {
	switch headerType {
	case "STATPOINT_DB":
		if version != 2 {
			return fmt.Errorf("unsupported statpoint_db version %d (want 2)", version)
		}
		return nil
	default:
		return fmt.Errorf("unexpected statpoint_db header type %q (want STATPOINT_DB)", headerType)
	}
}

// parseDoc decodes data into a yaml.Node tree, then extracts Header.Type,
// Header.Version, and the raw Body mapping nodes. Using yaml.Node directly
// (rather than decoding into typed structs) lets parseBodyRow report
// per-entry errors with the offending level in context.
func parseDoc(data []byte) (header, []*yaml.Node, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return header{}, nil, fmt.Errorf("parse document: %w", err)
	}
	if root.Kind == 0 {
		// Empty input stream: no document at all.
		return header{}, nil, nil
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

func parseBodyRow(row *yaml.Node) (Entry, error) {
	var entry Entry
	if row.Kind != yaml.MappingNode {
		return entry, fmt.Errorf("body mapping: expected mapping node, got kind %d", row.Kind)
	}
	for i := 0; i+1 < len(row.Content); i += 2 {
		key := row.Content[i].Value
		val := row.Content[i+1]
		switch key {
		case "Level":
			if val.Kind != yaml.ScalarNode {
				return entry, fmt.Errorf("level: expected scalar, got kind %d", val.Kind)
			}
			lvl, err := decodeUint32Value(val)
			if err != nil {
				return entry, &errInvalidPoints{raw: val.Value, err: fmt.Errorf("parse level: %w", err)}
			}
			entry.Level = lvl
		case "Points":
			if val.Kind != yaml.ScalarNode {
				return entry, fmt.Errorf("points: expected scalar, got kind %d", val.Kind)
			}
			pts, err := decodeInt32Value(val)
			if err != nil {
				return entry, err
			}
			entry.Points = pts
		}
	}
	return entry, nil
}

// errDuplicateLevel names the offending level so callers can match with
// errors.Is without parsing the message.
type errDuplicateLevel uint32

func (e errDuplicateLevel) Error() string {
	return fmt.Sprintf("duplicate level %d", uint32(e))
}

// Is matches: empty (wildcard) errDuplicateLevel matches any, otherwise the
// target must equal the receiver exactly.
func (e errDuplicateLevel) Is(target error) bool {
	t, ok := target.(errDuplicateLevel)
	return ok && (t == 0 || e == t)
}

var errEmptyLevel = errors.New("statpoint entry with empty (zero) level")

type errInvalidPoints struct {
	raw string
	err error
}

func (e *errInvalidPoints) Error() string {
	return fmt.Sprintf("invalid points %q: %v", e.raw, e.err)
}

func (e *errInvalidPoints) Unwrap() error { return e.err }

// Is matches when target is any non-nil *errInvalidPoints. This mirrors
// the constdb pattern: callers can use a zero-value pointer as a wildcard
// sentinel in errors.Is.
func (e *errInvalidPoints) Is(target error) bool {
	_, ok := target.(*errInvalidPoints)
	return ok
}

func decodeInt32Value(node *yaml.Node) (int32, error) {
	if node.Kind != yaml.ScalarNode {
		return 0, fmt.Errorf("expected scalar, got kind %d", node.Kind)
	}
	if node.Tag == "!!float" {
		return 0, fmt.Errorf("expected int, got float %q", node.Value)
	}
	v, err := strconv.ParseInt(node.Value, 10, 32)
	if err != nil {
		if v2, err2 := strconv.ParseInt(node.Value, 0, 32); err2 == nil {
			return int32(v2), nil
		}
		return 0, &errInvalidPoints{raw: node.Value, err: err}
	}
	return int32(v), nil
}

func decodeUint32Value(node *yaml.Node) (uint32, error) {
	if node.Kind != yaml.ScalarNode {
		return 0, fmt.Errorf("expected scalar, got kind %d", node.Kind)
	}
	if node.Tag == "!!float" {
		return 0, fmt.Errorf("expected int, got float %q", node.Value)
	}
	v, err := strconv.ParseUint(node.Value, 10, 32)
	if err != nil {
		if v2, err2 := strconv.ParseUint(node.Value, 0, 32); err2 == nil {
			return uint32(v2), nil
		}
		return 0, fmt.Errorf("parse int %q: %w", node.Value, err)
	}
	return uint32(v), nil
}

func resolveImportPath(base, ref string) string {
	if filepath.IsAbs(ref) {
		return filepath.Clean(ref)
	}
	return filepath.Clean(filepath.Join(base, ref))
}
