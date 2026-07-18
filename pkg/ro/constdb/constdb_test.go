//go:build unit

package constdb

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const validHeader = "Header:\n  Type: CONSTANT_DB\n  Version: 1\n"

func TestLoad_RealFile(t *testing.T) {
	path := filepath.Join("..", "..", "..", "third_party", "rathena", "db", "const.yml")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("rAthena submodule not available at %s: %v", path, err)
	}
	reg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if reg.Size() == 0 {
		t.Fatal("Size() = 0, want > 0")
	}
	for _, tc := range []struct {
		name string
		want int64
	}{
		{"SWORDCLAN", 1},
		{"ARCWANDCLAN", 2},
		{"REPUTATION_EP18", 3},
	} {
		got, ok := reg.Value(tc.name)
		if !ok {
			t.Errorf("Value(%q) not registered", tc.name)
			continue
		}
		if got != tc.want {
			t.Errorf("Value(%q) = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestLoad_DuplicateSameValue(t *testing.T) {
	in := validHeader + `Body:
  - {Name: DUP_OK, Value: 42}
  - {Name: DUP_OK, Value: 42}
`
	reg, err := LoadFileFromString(t, in)
	if err != nil {
		t.Fatalf("Load() error = %v (want idempotent success)", err)
	}
	if reg.Size() != 1 {
		t.Fatalf("Size() = %d, want 1", reg.Size())
	}
	got, ok := reg.Value("DUP_OK")
	if !ok || got != 42 {
		t.Errorf("Value(DUP_OK) = (%d, %v), want (42, true)", got, ok)
	}
}

func TestLoad_DuplicateConflict(t *testing.T) {
	in := validHeader + `Body:
  - {Name: DUP_BAD, Value: 1}
  - {Name: DUP_BAD, Value: 2}
`
	_, err := LoadFileFromString(t, in)
	if err == nil {
		t.Fatal("Load() error = nil, want duplicate conflict error")
	}
	if !strings.Contains(err.Error(), "duplicate constant") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "duplicate constant")
	}
	if !strings.Contains(err.Error(), "DUP_BAD") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "DUP_BAD")
	}
}

func TestLoad_ParameterFlag(t *testing.T) {
	in := validHeader + `Body:
  - {Name: PARAM_CONST, Value: 7, Parameter: true}
  - {Name: NORMAL_CONST, Value: 8}
`
	reg, err := LoadFileFromString(t, in)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	param, ok := reg.Lookup("PARAM_CONST")
	if !ok {
		t.Fatal("Lookup(PARAM_CONST) not registered")
	}
	if !param.Parameter {
		t.Errorf("PARAM_CONST Parameter = false, want true")
	}
	if param.Value != 7 {
		t.Errorf("PARAM_CONST Value = %d, want 7", param.Value)
	}
	norm, ok := reg.Lookup("NORMAL_CONST")
	if !ok {
		t.Fatal("Lookup(NORMAL_CONST) not registered")
	}
	if norm.Parameter {
		t.Errorf("NORMAL_CONST Parameter = true, want false (default)")
	}
	if norm.Value != 8 {
		t.Errorf("NORMAL_CONST Value = %d, want 8", norm.Value)
	}
}

func TestLoad_EmptyName(t *testing.T) {
	in := validHeader + `Body:
  - {Name: "", Value: 1}
`
	_, err := LoadFileFromString(t, in)
	if err == nil {
		t.Fatal("Load() error = nil, want empty-name error")
	}
	if !strings.Contains(err.Error(), "empty name") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "empty name")
	}
}

func TestLoad_InvalidValue(t *testing.T) {
	in := validHeader + `Body:
  - {Name: BAD_VAL, Value: not-a-number}
`
	_, err := LoadFileFromString(t, in)
	if err == nil {
		t.Fatal("Load() error = nil, want invalid-value error")
	}
	if !strings.Contains(err.Error(), "BAD_VAL") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "BAD_VAL")
	}
	if !strings.Contains(err.Error(), "invalid value") &&
		!strings.Contains(err.Error(), "cannot unmarshal") &&
		!strings.Contains(err.Error(), "cannot decode") {
		t.Errorf("error = %q, want it to describe the parse failure", err.Error())
	}
}

func TestLoad_NegativeValue(t *testing.T) {
	in := validHeader + `Body:
  - {Name: NEG_CONST, Value: -5}
`
	reg, err := LoadFileFromString(t, in)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	got, ok := reg.Value("NEG_CONST")
	if !ok {
		t.Fatal("Lookup(NEG_CONST) not registered")
	}
	if got != -5 {
		t.Errorf("Value(NEG_CONST) = %d, want -5", got)
	}
}

func TestLoadFile_Missing(t *testing.T) {
	reg := NewRegistry()
	err := reg.LoadFile(filepath.Join(t.TempDir(), "does_not_exist.yml"))
	if err == nil {
		t.Fatal("LoadFile() error = nil, want open error")
	}
	if !strings.Contains(err.Error(), "open const_db") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "open const_db")
	}
}

func TestLoadFile_Imports(t *testing.T) {
	dir := t.TempDir()
	imp := filepath.Join(dir, "extra.yml")
	imported := validHeader + `Body:
  - {Name: FROM_IMPORT, Value: 99}
`
	if err := os.WriteFile(imp, []byte(imported), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	main := validHeader + `Body:
  - {Name: FROM_MAIN, Value: 1}
Footer:
  Imports:
    - Path: extra.yml
    - Path: does_not_exist.yml
`
	mainPath := filepath.Join(dir, "main.yml")
	if err := os.WriteFile(mainPath, []byte(main), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	reg, err := LoadFile(mainPath)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if v, ok := reg.Value("FROM_MAIN"); !ok || v != 1 {
		t.Errorf("Value(FROM_MAIN) = (%d, %v), want (1, true)", v, ok)
	}
	if v, ok := reg.Value("FROM_IMPORT"); !ok || v != 99 {
		t.Errorf("Value(FROM_IMPORT) = (%d, %v), want (99, true)", v, ok)
	}
	if reg.Size() != 2 {
		t.Errorf("Size() = %d, want 2", reg.Size())
	}
}

func TestLookup_NotFound(t *testing.T) {
	reg, err := LoadFileFromString(t, validHeader+"Body:\n  - {Name: KNOWN, Value: 1}\n")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if _, ok := reg.Lookup("UNKNOWN_CONST"); ok {
		t.Error("Lookup(UNKNOWN_CONST) returned ok=true, want false")
	}
	if _, ok := reg.Value("UNKNOWN_CONST"); ok {
		t.Error("Value(UNKNOWN_CONST) returned ok=true, want false")
	}
}

func TestRegistry_NilSafe(t *testing.T) {
	var r *Registry
	if _, ok := r.Lookup("anything"); ok {
		t.Error("nil Lookup returned ok=true")
	}
	if _, ok := r.Value("anything"); ok {
		t.Error("nil Value returned ok=true")
	}
	if got := r.Size(); got != 0 {
		t.Errorf("nil Size = %d, want 0", got)
	}
}

func TestLoad_WrongHeaderType(t *testing.T) {
	in := `Header:
  Type: SKILL_DB
  Version: 4
Body:
  - {Name: ANY, Value: 1}
`
	_, err := LoadFileFromString(t, in)
	if err == nil {
		t.Fatal("Load() error = nil, want wrong header-type error")
	}
	if !strings.Contains(err.Error(), "unexpected Header.Type") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "unexpected Header.Type")
	}
}

func TestLoad_WrongHeaderVersion(t *testing.T) {
	in := `Header:
  Type: CONSTANT_DB
  Version: 99
Body:
  - {Name: ANY, Value: 1}
`
	_, err := LoadFileFromString(t, in)
	if err == nil {
		t.Fatal("Load() error = nil, want wrong version error")
	}
	if !strings.Contains(err.Error(), "unsupported Header.Version") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "unsupported Header.Version")
	}
}

func TestNewRegistry_Isolated(t *testing.T) {
	a := NewRegistry()
	b := NewRegistry()
	if err := a.Load(strings.NewReader(validHeader + "Body:\n  - {Name: X, Value: 1}\n")); err != nil {
		t.Fatalf("a.Load: %v", err)
	}
	if b.Size() != 0 {
		t.Errorf("b.Size() = %d after a loaded, want 0 (constructor isolation)", b.Size())
	}
}

func TestLoad_TruncatedYAML(t *testing.T) {
	reg := NewRegistry()
	err := reg.Load(strings.NewReader("Header: [invalid"))
	if err == nil {
		t.Fatal("Load() error = nil, want parse error")
	}
	if !strings.Contains(err.Error(), "parse const_db") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "parse const_db")
	}
	if !errors.Is(err, err) {
		t.Errorf("sanity: error must remain non-nil, got %v", err)
	}
}

func TestLoad_ReadFailure(t *testing.T) {
	reg := NewRegistry()
	err := reg.Load(failReader{})
	if err == nil {
		t.Fatal("Load() error = nil, want read error")
	}
	if !strings.Contains(err.Error(), "read const_db stream") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "read const_db stream")
	}
}

func TestLoad_DuplicateSameValueParameterUpgrade(t *testing.T) {
	in := validHeader + `Body:
  - {Name: UP, Value: 5}
  - {Name: UP, Value: 5, Parameter: true}
`
	reg, err := LoadFileFromString(t, in)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	got, ok := reg.Lookup("UP")
	if !ok {
		t.Fatal("Lookup(UP) not registered")
	}
	if !got.Parameter {
		t.Errorf("UP Parameter = false, want true (param upgrade idempotent reload)")
	}
	if got.Value != 5 {
		t.Errorf("UP Value = %d, want 5", got.Value)
	}
}

func TestErrDuplicate_Is(t *testing.T) {
	in := validHeader + `Body:
  - {Name: A, Value: 1}
  - {Name: A, Value: 2}
`
	_, err := LoadFileFromString(t, in)
	if err == nil {
		t.Fatal("Load() error = nil")
	}
	if !errors.Is(err, errDuplicate("A")) {
		t.Errorf("errors.Is(err, errDuplicate(A)) = false, want true (got %T: %v)", err, err)
	}
}

func TestErrInvalidValue_Unwrap(t *testing.T) {
	in := validHeader + `Body:
  - {Name: BAD, Value: not-a-number}
`
	_, err := LoadFileFromString(t, in)
	if err == nil {
		t.Fatal("Load() error = nil")
	}
	var typed *errInvalidValue
	if !errors.As(err, &typed) {
		t.Fatalf("errors.As(*errInvalidValue) = false, want true (got %T)", err)
	}
	if typed.name != "BAD" {
		t.Errorf("typed.name = %q, want BAD", typed.name)
	}
}

func TestLoad_HeaderKindWrong(t *testing.T) {
	reg := NewRegistry()
	err := reg.Load(strings.NewReader("- 1\n- 2\n"))
	if err == nil {
		t.Fatal("Load() error = nil, want parse-error")
	}
	if !strings.Contains(err.Error(), "parse const_db") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "parse const_db")
	}
}

func TestValidateHeader_OtherError(t *testing.T) {
	if err := validateHeader("MY_DB", 7); err == nil || !strings.Contains(err.Error(), "unexpected Header.Type") {
		t.Errorf("validateHeader other type: err = %v, want unexpected Header.Type", err)
	}
}

func TestResolveImportPath(t *testing.T) {
	cases := []struct {
		base, ref, want string
	}{
		{"/a/b", "c.yml", "/a/b/c.yml"},
		{"/a/b", "../c.yml", "/a/c.yml"},
		{"/a/b", "/abs.yml", "/abs.yml"},
	}
	for _, tc := range cases {
		got := resolveImportPath(tc.base, tc.ref)
		if got != tc.want {
			t.Errorf("resolveImportPath(%q, %q) = %q, want %q", tc.base, tc.ref, got, tc.want)
		}
	}
}

func TestErrEmptyName_Is(t *testing.T) {
	in := validHeader + `Body:
  - {Name: "", Value: 1}
`
	_, err := LoadFileFromString(t, in)
	if err == nil {
		t.Fatal("Load() error = nil")
	}
	if !errors.Is(err, errEmptyName) {
		t.Errorf("errors.Is(err, errEmptyName) = false, want true (got %v)", err)
	}
}

func TestErrInvalidValue_Is(t *testing.T) {
	in := validHeader + `Body:
  - {Name: B, Value: not-int}
`
	_, err := LoadFileFromString(t, in)
	if err == nil {
		t.Fatal("Load() error = nil")
	}
	var typed *errInvalidValue
	if !errors.Is(err, typed) {
		t.Errorf("errors.Is(err, &errInvalidValue{}) = false, want true")
	}
}

func TestLoad_HeaderNotMapping(t *testing.T) {
	in := `Header: 42
Body:
  - {Name: X, Value: 1}
`
	_, err := LoadFileFromString(t, in)
	if err == nil {
		t.Fatal("Load() error = nil")
	}
	if !strings.Contains(err.Error(), "parse const_db") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "parse const_db")
	}
}

func TestLoad_BodyNotSequence(t *testing.T) {
	in := validHeader + `Body:
  X: 1
`
	_, err := LoadFileFromString(t, in)
	if err == nil {
		t.Fatal("Load() error = nil")
	}
	if !strings.Contains(err.Error(), "expected sequence") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "expected sequence")
	}
}

func TestDecodeInt64Value_FloatTag(t *testing.T) {
	// Hand-build a scalar with !!float tag and feed decodeInt64Value directly.
	var root yaml.Node
	if err := yaml.Unmarshal([]byte("1.5"), &root); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	scalar := root.Content[0]
	if _, err := decodeInt64Value(scalar); err == nil || !strings.Contains(err.Error(), "float") {
		t.Errorf("decodeInt64Value(float) err = %v, want float rejection", err)
	}
}

func TestDecodeInt64Value_NonScalar(t *testing.T) {
	// Build a mapping node and feed it to decodeInt64Value to hit the
	// non-scalar branch.
	var root yaml.Node
	if err := yaml.Unmarshal([]byte("{a: 1}"), &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mapping := root.Content[0]
	if _, err := decodeInt64Value(mapping); err == nil || !strings.Contains(err.Error(), "expected scalar") {
		t.Errorf("decodeInt64Value(mapping) err = %v, want non-scalar rejection", err)
	}
}

func TestParseBodyRow_NonScalarName(t *testing.T) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte("- {Name: {a: 1}, Value: 1}\n"), &root); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	bodySeq := root.Content[0]
	row := bodySeq.Content[0]
	if _, err := parseBodyRow(row); err == nil || !strings.Contains(err.Error(), "name: expected scalar") {
		t.Errorf("parseBodyRow(Name=map) err = %v, want Name expected scalar", err)
	}
}

func TestParseBodyRow_NonScalarParameter(t *testing.T) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte("- {Name: P, Value: 1, Parameter: {x: true}}\n"), &root); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	bodySeq := root.Content[0]
	row := bodySeq.Content[0]
	if _, err := parseBodyRow(row); err == nil || !strings.Contains(err.Error(), "parameter: expected scalar") {
		t.Errorf("parseBodyRow(Parameter=map) err = %v, want Parameter expected scalar", err)
	}
}

func TestParseBodyRow_BadParameterBool(t *testing.T) {
	in := validHeader + `Body:
  - {Name: P, Value: 1, Parameter: notabool}
`
	_, err := LoadFileFromString(t, in)
	if err == nil {
		t.Fatal("Load() error = nil")
	}
	if !strings.Contains(err.Error(), "parse parameter:") {
		t.Errorf("error = %q, want to contain parse parameter:", err.Error())
	}
}

func TestLoadFile_RecursiveImportErrorWrapped(t *testing.T) {
	dir := t.TempDir()
	imp := filepath.Join(dir, "bad.yml")
	if err := os.WriteFile(imp, []byte("Header: [invalid\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	main := validHeader + `Body:
  - {Name: A, Value: 1}
Footer:
  Imports:
    - Path: bad.yml
`
	mainPath := filepath.Join(dir, "main.yml")
	if err := os.WriteFile(mainPath, []byte(main), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := LoadFile(mainPath)
	if err == nil {
		t.Fatal("LoadFile() error = nil, want recursive import error")
	}
	if !strings.Contains(err.Error(), "load const_db import") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "load const_db import")
	}
}

func TestLoadFile_StatErrorPath(t *testing.T) {
	// Real file at a stat-able path that LoadFile rejects -- use a directory.
	dir := t.TempDir()
	imp := filepath.Join(dir, "dirImport.yml")
	if err := os.Mkdir(imp, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	main := validHeader + `Body:
  - {Name: A, Value: 1}
Footer:
  Imports:
    - Path: dirImport.yml
`
	mainPath := filepath.Join(dir, "main.yml")
	if err := os.WriteFile(mainPath, []byte(main), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// os.Open on a directory fails on Linux/macOS but Stat succeeds -> LoadFile fails to open -> wrapped error surfaces
	_, err := LoadFile(mainPath)
	if err == nil {
		t.Fatal("LoadFile() error = nil, want open-of-dir error")
	}
	if !strings.Contains(err.Error(), "load const_db import") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "load const_db import")
	}
}

// failReader returns an error on every Read.
type failReader struct{}

func (failReader) Read(_ []byte) (int, error) { return 0, errors.New("disk on fire") }

// LoadFileFromString is a test helper that writes input to a temp file and
// returns LoadFile's result. It exists to keep individual tests DRY without
// exposing the temp-file dance in every test case.
func LoadFileFromString(t *testing.T, in string) (*Registry, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "const.yml")
	if err := os.WriteFile(path, []byte(in), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return LoadFile(path)
}

// drainReader returns an io.Reader whose Read always returns io.EOF. Used to
// verify Load surfaces decoder-side errors as wrapped parse errors.

func TestLoadFile_SelfImport(t *testing.T) {
	dir := t.TempDir()
	main := validHeader + `Body:
  - {Name: X, Value: 1}
Footer:
  Imports:
    - Path: main.yml
`
	mainPath := filepath.Join(dir, "main.yml")
	if err := os.WriteFile(mainPath, []byte(main), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := LoadFile(mainPath)
	if err == nil {
		t.Fatal("LoadFile() error = nil, want circular-import error")
	}
	if !strings.Contains(err.Error(), "circular const_db import detected") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "circular const_db import detected")
	}
}

func TestLoadFile_CircularImport(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.yml")
	bPath := filepath.Join(dir, "b.yml")

	a := validHeader + `Body:
  - {Name: FROM_A, Value: 1}
Footer:
  Imports:
    - Path: b.yml
`
	b := validHeader + `Body:
  - {Name: FROM_B, Value: 2}
Footer:
  Imports:
    - Path: a.yml
`
	if err := os.WriteFile(aPath, []byte(a), 0o600); err != nil {
		t.Fatalf("WriteFile a: %v", err)
	}
	if err := os.WriteFile(bPath, []byte(b), 0o600); err != nil {
		t.Fatalf("WriteFile b: %v", err)
	}
	_, err := LoadFile(aPath)
	if err == nil {
		t.Fatal("LoadFile() error = nil, want circular-import error")
	}
	if !strings.Contains(err.Error(), "circular const_db import detected") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "circular const_db import detected")
	}
}

func TestErrDuplicate_PreciseIs(t *testing.T) {
	errA := errDuplicate("A")
	errB := errDuplicate("B")
	if errors.Is(errA, errB) {
		t.Error(`errors.Is(errA, errB) = true, want false (precise matching)`)
	}
	if !errors.Is(errA, errA) {
		t.Error("errors.Is(errA, errA) = false, want true")
	}
	if !errors.Is(errA, errDuplicate("")) {
		t.Error(`errors.Is(errA, errDuplicate("")) = false, want true (wildcard)`)
	}
	if !errors.Is(errDuplicate("A"), errDuplicate("")) {
		t.Error(`errors.Is(errDuplicate("A"), errDuplicate("")) = false, want true (wildcard)`)
	}
}

func TestDecode_HexValue(t *testing.T) {
	in := validHeader + `Body:
  - {Name: HEX_FLAG, Value: 0x100}
`
	reg, err := LoadFileFromString(t, in)
	if err != nil {
		t.Fatalf("LoadFileFromString() error = %v", err)
	}
	got, ok := reg.Value("HEX_FLAG")
	if !ok {
		t.Fatal("Lookup(HEX_FLAG) not registered")
	}
	if got != 256 {
		t.Errorf("Value(HEX_FLAG) = %d, want 256", got)
	}
}

func TestDecode_BinaryValue(t *testing.T) {
	in := validHeader + `Body:
  - {Name: BIN_VAL, Value: 0b1010}
`
	reg, err := LoadFileFromString(t, in)
	if err != nil {
		t.Fatalf("LoadFileFromString() error = %v", err)
	}
	got, ok := reg.Value("BIN_VAL")
	if !ok {
		t.Fatal("Lookup(BIN_VAL) not registered")
	}
	if got != 10 {
		t.Errorf("Value(BIN_VAL) = %d, want 10", got)
	}
}

func TestDecode_OctalValue(t *testing.T) {
	in := validHeader + `Body:
  - {Name: OCT_VAL, Value: 0o17}
`
	reg, err := LoadFileFromString(t, in)
	if err != nil {
		t.Fatalf("LoadFileFromString() error = %v", err)
	}
	got, ok := reg.Value("OCT_VAL")
	if !ok {
		t.Fatal("Lookup(OCT_VAL) not registered")
	}
	if got != 15 {
		t.Errorf("Value(OCT_VAL) = %d, want 15", got)
	}
}
