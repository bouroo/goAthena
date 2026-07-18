//go:build unit

package statpoint

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const validHeader = "Header:\n  Type: STATPOINT_DB\n  Version: 2\n"

func TestLoad_RealFile(t *testing.T) {
	path := filepath.Join("..", "..", "..", "third_party", "rathena", "db", "pre-re", "statpoint.yml")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("rAthena submodule not available at %s: %v", path, err)
	}
	reg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	cases := []struct {
		level uint32
		want  int32
	}{
		{1, 48},
		{2, 51},
		{3, 54},
		{10, 80},
		{20, 135},
	}
	for _, tc := range cases {
		got, ok := reg.Points(tc.level)
		if !ok {
			t.Errorf("Points(%d) not registered", tc.level)
			continue
		}
		if got != tc.want {
			t.Errorf("Points(%d) = %d, want %d", tc.level, got, tc.want)
		}
	}
	if reg.MaxLevel() < 99 {
		t.Errorf("MaxLevel() = %d, want >= 99 (pre-re file)", reg.MaxLevel())
	}
	if reg.Size() != int(reg.MaxLevel()) {
		t.Errorf("Size() = %d, MaxLevel() = %d, want Size() == MaxLevel() (every level 1..MaxLevel present)", reg.Size(), reg.MaxLevel())
	}
	lvls := reg.Levels()
	for i := 1; i < len(lvls); i++ {
		if lvls[i] <= lvls[i-1] {
			t.Fatalf("Levels() not sorted ascending at index %d: %d <= %d", i, lvls[i], lvls[i-1])
		}
	}
}

func TestLoad_Empty(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Load(strings.NewReader("")); err != nil {
		t.Fatalf("Load(empty) error = %v", err)
	}
	if reg.Size() != 0 {
		t.Errorf("Size() = %d, want 0", reg.Size())
	}
	if got, ok := reg.Points(1); ok {
		t.Errorf("Points(1) = (%d, true), want (_, false)", got)
	}
	if reg.MaxLevel() != 0 {
		t.Errorf("MaxLevel() = %d, want 0", reg.MaxLevel())
	}
	if got := reg.Levels(); len(got) != 0 {
		t.Errorf("Levels() len = %d, want 0", len(got))
	}
}

func TestLoad_DuplicateSamePoints(t *testing.T) {
	in := validHeader + `Body:
  - {Level: 5, Points: 60}
  - {Level: 5, Points: 60}
`
	reg, err := LoadFileFromString(t, in)
	if err != nil {
		t.Fatalf("LoadFileFromString() error = %v (want idempotent success)", err)
	}
	if reg.Size() != 1 {
		t.Fatalf("Size() = %d, want 1", reg.Size())
	}
	got, ok := reg.Points(5)
	if !ok || got != 60 {
		t.Errorf("Points(5) = (%d, %v), want (60, true)", got, ok)
	}
}

func TestLoad_DuplicateConflict(t *testing.T) {
	in := validHeader + `Body:
  - {Level: 5, Points: 60}
  - {Level: 5, Points: 61}
`
	_, err := LoadFileFromString(t, in)
	if err == nil {
		t.Fatal("LoadFileFromString() error = nil, want duplicate conflict error")
	}
	if !strings.Contains(err.Error(), "duplicate level") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "duplicate level")
	}
	if !strings.Contains(err.Error(), "5") {
		t.Errorf("error = %q, want it to contain level 5", err.Error())
	}
}

func TestLoad_EmptyLevel(t *testing.T) {
	in := validHeader + `Body:
  - {Level: 0, Points: 10}
`
	_, err := LoadFileFromString(t, in)
	if err == nil {
		t.Fatal("LoadFileFromString() error = nil, want empty-level error")
	}
	if !strings.Contains(err.Error(), "empty") || !strings.Contains(err.Error(), "level") {
		t.Errorf("error = %q, want it to describe empty level", err.Error())
	}
	if !errors.Is(err, errEmptyLevel) {
		t.Errorf("errors.Is(err, errEmptyLevel) = false, want true (got %v)", err)
	}
}

func TestLoad_InvalidPoints(t *testing.T) {
	in := validHeader + `Body:
  - {Level: 1, Points: abc}
`
	_, err := LoadFileFromString(t, in)
	if err == nil {
		t.Fatal("LoadFileFromString() error = nil, want invalid-points error")
	}
	if !strings.Contains(err.Error(), "invalid points") &&
		!strings.Contains(err.Error(), "abc") {
		t.Errorf("error = %q, want it to describe invalid points", err.Error())
	}
	var typed *errInvalidPoints
	if !errors.As(err, &typed) {
		t.Fatalf("errors.As(*errInvalidPoints) = false, want true (got %T)", err)
	}
	if typed.raw != "abc" {
		t.Errorf("typed.raw = %q, want abc", typed.raw)
	}
}

func TestLoad_NegativePoints(t *testing.T) {
	in := validHeader + `Body:
  - {Level: 1, Points: -5}
  - {Level: 2, Points: 48}
`
	reg, err := LoadFileFromString(t, in)
	if err != nil {
		t.Fatalf("LoadFileFromString() error = %v", err)
	}
	got, ok := reg.Points(1)
	if !ok || got != -5 {
		t.Errorf("Points(1) = (%d, %v), want (-5, true)", got, ok)
	}
	got, ok = reg.Points(2)
	if !ok || got != 48 {
		t.Errorf("Points(2) = (%d, %v), want (48, true)", got, ok)
	}
}

func TestLoad_HexPoints(t *testing.T) {
	in := validHeader + `Body:
  - {Level: 1, Points: 0x40}
`
	reg, err := LoadFileFromString(t, in)
	if err != nil {
		t.Fatalf("LoadFileFromString() error = %v", err)
	}
	got, ok := reg.Points(1)
	if !ok || got != 64 {
		t.Errorf("Points(1) = (%d, %v), want (64, true)", got, ok)
	}
}

func TestLoad_WrongHeaderType(t *testing.T) {
	in := `Header:
  Type: WRONG_DB
  Version: 2
Body:
  - {Level: 1, Points: 1}
`
	_, err := LoadFileFromString(t, in)
	if err == nil {
		t.Fatal("LoadFileFromString() error = nil, want wrong header-type error")
	}
	if !strings.Contains(err.Error(), "statpoint_db header") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "statpoint_db header")
	}
}

func TestLoad_WrongHeaderVersion(t *testing.T) {
	in := `Header:
  Type: STATPOINT_DB
  Version: 1
Body:
  - {Level: 1, Points: 1}
`
	_, err := LoadFileFromString(t, in)
	if err == nil {
		t.Fatal("LoadFileFromString() error = nil, want wrong version error")
	}
	if !strings.Contains(err.Error(), "statpoint_db version") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "statpoint_db version")
	}
}

func TestLoadFile_Missing(t *testing.T) {
	reg := NewRegistry()
	err := reg.LoadFile(filepath.Join(t.TempDir(), "does_not_exist.yml"))
	if err == nil {
		t.Fatal("LoadFile() error = nil, want open error")
	}
	if !strings.Contains(err.Error(), "open statpoint_db") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "open statpoint_db")
	}
}

func TestLoadFile_Imports(t *testing.T) {
	dir := t.TempDir()
	imp := filepath.Join(dir, "extra.yml")
	imported := validHeader + `Body:
  - {Level: 99, Points: 999}
`
	if err := os.WriteFile(imp, []byte(imported), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	main := validHeader + `Body:
  - {Level: 1, Points: 48}
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
	if v, ok := reg.Points(1); !ok || v != 48 {
		t.Errorf("Points(1) = (%d, %v), want (48, true)", v, ok)
	}
	if v, ok := reg.Points(99); !ok || v != 999 {
		t.Errorf("Points(99) = (%d, %v), want (999, true)", v, ok)
	}
	if reg.Size() != 2 {
		t.Errorf("Size() = %d, want 2", reg.Size())
	}
}

func TestLoadFile_SelfImport(t *testing.T) {
	dir := t.TempDir()
	main := validHeader + `Body:
  - {Level: 1, Points: 1}
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
	if !strings.Contains(err.Error(), "circular statpoint_db import") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "circular statpoint_db import")
	}
}

func TestLoadFile_CircularImport(t *testing.T) {
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.yml")
	bPath := filepath.Join(dir, "b.yml")

	a := validHeader + `Body:
  - {Level: 1, Points: 1}
Footer:
  Imports:
    - Path: b.yml
`
	b := validHeader + `Body:
  - {Level: 2, Points: 2}
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
	if !strings.Contains(err.Error(), "circular statpoint_db import") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "circular statpoint_db import")
	}
}

func TestLookup_NotFound(t *testing.T) {
	reg, err := LoadFileFromString(t, validHeader+"Body:\n  - {Level: 1, Points: 48}\n")
	if err != nil {
		t.Fatalf("LoadFileFromString() error = %v", err)
	}
	if v, ok := reg.Points(999); ok {
		t.Errorf("Points(999) = (%d, true), want (_, false)", v)
	}
}

func TestNil_Receiver(t *testing.T) {
	var r *Registry
	if err := r.Load(strings.NewReader("")); err == nil {
		t.Error("nil.Load() error = nil, want error")
	}
	if err := r.LoadFile("anything.yml"); err == nil {
		t.Error("nil.LoadFile() error = nil, want error")
	}
	if v, ok := r.Points(1); ok {
		t.Errorf("nil.Points(1) = (%d, true), want (_, false)", v)
	}
	if got := r.MaxLevel(); got != 0 {
		t.Errorf("nil.MaxLevel() = %d, want 0", got)
	}
	if got := r.Size(); got != 0 {
		t.Errorf("nil.Size() = %d, want 0", got)
	}
	if got := r.Levels(); len(got) != 0 {
		t.Errorf("nil.Levels() len = %d, want 0", len(got))
	}
}

func TestNewRegistry_Isolated(t *testing.T) {
	a := NewRegistry()
	b := NewRegistry()
	if err := a.Load(strings.NewReader(validHeader + "Body:\n  - {Level: 1, Points: 48}\n")); err != nil {
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
	if !strings.Contains(err.Error(), "parse statpoint_db") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "parse statpoint_db")
	}
}

func TestLoad_ReadFailure(t *testing.T) {
	reg := NewRegistry()
	err := reg.Load(failReader{})
	if err == nil {
		t.Fatal("Load() error = nil, want read error")
	}
	if !strings.Contains(err.Error(), "read statpoint_db stream") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "read statpoint_db stream")
	}
}

func TestErrDuplicateLevel_Is(t *testing.T) {
	in := validHeader + `Body:
  - {Level: 7, Points: 60}
  - {Level: 7, Points: 61}
`
	_, err := LoadFileFromString(t, in)
	if err == nil {
		t.Fatal("LoadFileFromString() error = nil")
	}
	if !errors.Is(err, errDuplicateLevel(7)) {
		t.Errorf("errors.Is(err, errDuplicateLevel(7)) = false, want true (got %v)", err)
	}
	if errors.Is(err, errDuplicateLevel(8)) {
		t.Error("errors.Is(err, errDuplicateLevel(8)) = true, want false (precise)")
	}
	if !errors.Is(err, errDuplicateLevel(0)) {
		t.Error("errors.Is(err, errDuplicateLevel(0)) = false, want true (wildcard)")
	}
}

func TestErrEmptyLevel_Is(t *testing.T) {
	in := validHeader + `Body:
  - {Level: 0, Points: 1}
`
	_, err := LoadFileFromString(t, in)
	if err == nil {
		t.Fatal("LoadFileFromString() error = nil")
	}
	if !errors.Is(err, errEmptyLevel) {
		t.Errorf("errors.Is(err, errEmptyLevel) = false, want true (got %v)", err)
	}
}

func TestErrInvalidPoints_Is(t *testing.T) {
	in := validHeader + `Body:
  - {Level: 1, Points: not-int}
`
	_, err := LoadFileFromString(t, in)
	if err == nil {
		t.Fatal("LoadFileFromString() error = nil")
	}
	var typed *errInvalidPoints
	if !errors.Is(err, typed) {
		t.Errorf("errors.Is(err, &errInvalidPoints{}) = false, want true (got %v)", err)
	}
}

func TestLoad_HeaderKindWrong(t *testing.T) {
	reg := NewRegistry()
	err := reg.Load(strings.NewReader("- 1\n- 2\n"))
	if err == nil {
		t.Fatal("Load() error = nil, want parse-error")
	}
	if !strings.Contains(err.Error(), "parse statpoint_db") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "parse statpoint_db")
	}
}

func TestValidateHeader_OtherError(t *testing.T) {
	if err := validateHeader("MY_DB", 7); err == nil || !strings.Contains(err.Error(), "statpoint_db header") {
		t.Errorf("validateHeader other type: err = %v, want statpoint_db header", err)
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

func TestLoad_HeaderNotMapping(t *testing.T) {
	in := `Header: 42
Body:
  - {Level: 1, Points: 1}
`
	_, err := LoadFileFromString(t, in)
	if err == nil {
		t.Fatal("LoadFileFromString() error = nil")
	}
	if !strings.Contains(err.Error(), "parse statpoint_db") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "parse statpoint_db")
	}
}

func TestLoad_BodyNotSequence(t *testing.T) {
	in := validHeader + `Body:
  X: 1
`
	_, err := LoadFileFromString(t, in)
	if err == nil {
		t.Fatal("LoadFileFromString() error = nil")
	}
	if !strings.Contains(err.Error(), "expected sequence") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "expected sequence")
	}
}

func TestDecodeInt32Value_FloatTag(t *testing.T) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte("1.5"), &root); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	scalar := root.Content[0]
	if _, err := decodeInt32Value(scalar); err == nil || !strings.Contains(err.Error(), "float") {
		t.Errorf("decodeInt32Value(float) err = %v, want float rejection", err)
	}
}

func TestDecodeInt32Value_NonScalar(t *testing.T) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte("{a: 1}"), &root); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	mapping := root.Content[0]
	if _, err := decodeInt32Value(mapping); err == nil || !strings.Contains(err.Error(), "expected scalar") {
		t.Errorf("decodeInt32Value(mapping) err = %v, want non-scalar rejection", err)
	}
}

func TestParseBodyRow_NonScalarLevel(t *testing.T) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte("- {Level: {a: 1}, Points: 1}\n"), &root); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	bodySeq := root.Content[0]
	row := bodySeq.Content[0]
	if _, err := parseBodyRow(row); err == nil || !strings.Contains(err.Error(), "level: expected scalar") {
		t.Errorf("parseBodyRow(Level=map) err = %v, want Level expected scalar", err)
	}
}

func TestParseBodyRow_NonScalarPoints(t *testing.T) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte("- {Level: 1, Points: {x: true}}\n"), &root); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	bodySeq := root.Content[0]
	row := bodySeq.Content[0]
	if _, err := parseBodyRow(row); err == nil || !strings.Contains(err.Error(), "points: expected scalar") {
		t.Errorf("parseBodyRow(Points=map) err = %v, want Points expected scalar", err)
	}
}

func TestLoadFile_RecursiveImportErrorWrapped(t *testing.T) {
	dir := t.TempDir()
	imp := filepath.Join(dir, "bad.yml")
	if err := os.WriteFile(imp, []byte("Header: [invalid\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	main := validHeader + `Body:
  - {Level: 1, Points: 1}
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
	if !strings.Contains(err.Error(), "load statpoint_db import") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "load statpoint_db import")
	}
}

func TestLevels_ReturnsDefensiveCopy(t *testing.T) {
	in := validHeader + `Body:
  - {Level: 3, Points: 30}
  - {Level: 1, Points: 10}
  - {Level: 2, Points: 20}
`
	reg, err := LoadFileFromString(t, in)
	if err != nil {
		t.Fatalf("LoadFileFromString() error = %v", err)
	}
	a := reg.Levels()
	b := reg.Levels()
	if len(a) != 3 || a[0] != 1 || a[1] != 2 || a[2] != 3 {
		t.Errorf("first Levels() = %v, want [1 2 3]", a)
	}
	if len(b) != 3 || b[0] != 1 || b[1] != 2 || b[2] != 3 {
		t.Errorf("second Levels() = %v, want [1 2 3]", b)
	}
	a[0] = 999
	c := reg.Levels()
	if c[0] == 999 {
		t.Error("mutating returned slice affected registry (Levels() not defensive)")
	}
}

func TestLoadFile_MissingImportSilent(t *testing.T) {
	dir := t.TempDir()
	main := validHeader + `Body:
  - {Level: 1, Points: 1}
Footer:
  Imports:
    - Path: nope.yml
`
	mainPath := filepath.Join(dir, "main.yml")
	if err := os.WriteFile(mainPath, []byte(main), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	reg, err := LoadFile(mainPath)
	if err != nil {
		t.Fatalf("LoadFile() error = %v (missing import should be silently skipped)", err)
	}
	if v, ok := reg.Points(1); !ok || v != 1 {
		t.Errorf("Points(1) = (%d, %v), want (1, true)", v, ok)
	}
}

func TestLoadFile_DiamondImports(t *testing.T) {
	dir := t.TempDir()
	cPath := filepath.Join(dir, "c.yml")
	aPath := filepath.Join(dir, "a.yml")
	bPath := filepath.Join(dir, "b.yml")
	a := validHeader + `Body:
  - {Level: 1, Points: 10}
Footer:
  Imports:
    - Path: b.yml
    - Path: c.yml
`
	b := validHeader + `Body:
  - {Level: 2, Points: 20}
Footer:
  Imports:
    - Path: c.yml
`
	cBody := validHeader + `Body:
  - {Level: 3, Points: 30}
`
	if err := os.WriteFile(aPath, []byte(a), 0o600); err != nil {
		t.Fatalf("WriteFile a: %v", err)
	}
	if err := os.WriteFile(bPath, []byte(b), 0o600); err != nil {
		t.Fatalf("WriteFile b: %v", err)
	}
	if err := os.WriteFile(cPath, []byte(cBody), 0o600); err != nil {
		t.Fatalf("WriteFile c: %v", err)
	}
	reg, err := LoadFile(aPath)
	if err != nil {
		t.Fatalf("LoadFile() error = %v (diamond import should succeed with recursion-stack tracking)", err)
	}
	for _, tc := range []struct {
		level uint32
		want  int32
	}{
		{1, 10},
		{2, 20},
		{3, 30},
	} {
		got, ok := reg.Points(tc.level)
		if !ok {
			t.Errorf("Points(%d) not registered after diamond load", tc.level)
			continue
		}
		if got != tc.want {
			t.Errorf("Points(%d) = %d, want %d", tc.level, got, tc.want)
		}
	}
	if reg.Size() != 3 {
		t.Errorf("Size() = %d, want 3 (all three distinct levels)", reg.Size())
	}
	if got, want := reg.Levels(), []uint32{1, 2, 3}; !slicesEqual32(got, want) {
		t.Errorf("Levels() = %v, want %v", got, want)
	}
}

func TestLoad_EmptyYAMLStream(t *testing.T) {
	cases := []struct {
		name, in string
	}{
		{"empty", ""},
		{"comment-only", "# only a comment\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := NewRegistry()
			if err := reg.Load(strings.NewReader(tc.in)); err != nil {
				t.Fatalf("Load(%q) error = %v, want nil", tc.in, err)
			}
			if reg.Size() != 0 {
				t.Errorf("Size() = %d, want 0", reg.Size())
			}
			if got := reg.MaxLevel(); got != 0 {
				t.Errorf("MaxLevel() = %d, want 0", got)
			}
			if got := reg.Levels(); len(got) != 0 {
				t.Errorf("Levels() len = %d, want 0", len(got))
			}
		})
	}
}

func TestLoadFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yml")
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	reg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile(empty) error = %v, want nil", err)
	}
	if reg.Size() != 0 {
		t.Errorf("Size() = %d, want 0", reg.Size())
	}
	if got := reg.MaxLevel(); got != 0 {
		t.Errorf("MaxLevel() = %d, want 0", got)
	}
}

func TestLoadFile_CommentOnlyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "comments.yml")
	if err := os.WriteFile(path, []byte("# only a comment\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	reg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile(comments) error = %v, want nil", err)
	}
	if reg.Size() != 0 {
		t.Errorf("Size() = %d, want 0", reg.Size())
	}
	if got := reg.MaxLevel(); got != 0 {
		t.Errorf("MaxLevel() = %d, want 0", got)
	}
}

func TestLevels_Sorted(t *testing.T) {
	reg := NewRegistry()
	for _, lvl := range []uint32{5, 2, 9, 1} {
		in := validHeader + fmt.Sprintf("Body:\n  - {Level: %d, Points: %d}\n", lvl, int32(lvl)*10)
		if err := reg.Load(strings.NewReader(in)); err != nil {
			t.Fatalf("Load(level %d): %v", lvl, err)
		}
	}
	got := reg.Levels()
	want := []uint32{1, 2, 5, 9}
	if len(got) != len(want) {
		t.Fatalf("Levels() len = %d (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Levels()[%d] = %d, want %d (full slice %v)", i, got[i], want[i], got)
		}
	}
}

func slicesEqual32(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// failReader returns an error on every Read.
type failReader struct{}

func (failReader) Read(_ []byte) (int, error) { return 0, errors.New("disk on fire") }

// LoadFileFromString is a test helper that writes input to a temp file and
// returns LoadFile's result. Keeps individual tests DRY.
func LoadFileFromString(t *testing.T, in string) (*Registry, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "statpoint.yml")
	if err := os.WriteFile(path, []byte(in), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return LoadFile(path)
}
