//go:build unit

package mapindex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_RealFile(t *testing.T) {
	path := filepath.Join("..", "..", "..", "third_party", "rathena", "db", "map_index.txt")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("rAthena submodule not available at %s: %v", path, err)
	}
	reg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if reg.Len() <= 900 {
		t.Fatalf("Len() = %d, want more than 900", reg.Len())
	}

	if idx, ok := reg.Get("alb_ship"); !ok || idx != 1 {
		t.Errorf("Get(alb_ship) = (%d, %v), want (1, true)", idx, ok)
	}
	if idx, ok := reg.Get("alb2trea"); !ok || idx != 2 {
		t.Errorf("Get(alb2trea) = (%d, %v), want (2, true)", idx, ok)
	}
	if idx, ok := reg.Get("alberta"); !ok || idx != 3 {
		t.Errorf("Get(alberta) = (%d, %v), want (3, true)", idx, ok)
	}
	if _, ok := reg.Get("nonexistent_map"); ok {
		t.Error("Get(nonexistent_map) returned ok=true, want false")
	}

	if name, ok := reg.NameAt(1); !ok || name != "alb_ship" {
		t.Errorf("NameAt(1) = (%q, %v), want (alb_ship, true)", name, ok)
	}
	if _, ok := reg.NameAt(0); ok {
		t.Error("NameAt(0) returned ok=true, want false (index 0 reserved)")
	}
}

func TestLoad_BasicAutoIncrement(t *testing.T) {
	input := "map1 10\nmap2\nmap3\n"
	reg, err := Load(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, tc := range []struct {
		name string
		want int
	}{
		{"map1", 10},
		{"map2", 11},
		{"map3", 12},
	} {
		got, ok := reg.Get(tc.name)
		if !ok {
			t.Errorf("Get(%q) missing", tc.name)
			continue
		}
		if got != tc.want {
			t.Errorf("Get(%q) = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestLoad_ExplicitIndexReset(t *testing.T) {
	input := "firstmap 5\nsecondmap\nthirdmap 100\nfourthmap\n"
	reg, err := Load(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, tc := range []struct {
		name string
		want int
	}{
		{"firstmap", 5},
		{"secondmap", 6},
		{"thirdmap", 100},
		{"fourthmap", 101},
	} {
		got, ok := reg.Get(tc.name)
		if !ok {
			t.Errorf("Get(%q) missing", tc.name)
			continue
		}
		if got != tc.want {
			t.Errorf("Get(%q) = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestLoad_DuplicateName(t *testing.T) {
	input := "mapA 1\nmapB 2\nmapA 3\n"
	_, err := Load(strings.NewReader(input))
	if err == nil {
		t.Fatal("Load() error = nil, want duplicate name error")
	}
	if !strings.Contains(err.Error(), "duplicate map name") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "duplicate map name")
	}
}

func TestLoad_DuplicateIndex(t *testing.T) {
	input := "mapA 5\nmapB 5\n"
	_, err := Load(strings.NewReader(input))
	if err == nil {
		t.Fatal("Load() error = nil, want duplicate index error")
	}
	if !strings.Contains(err.Error(), "duplicate index") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "duplicate index")
	}
}

func TestLoad_IndexZeroRejected(t *testing.T) {
	input := "badmap 0\n"
	_, err := Load(strings.NewReader(input))
	if err == nil {
		t.Fatal("Load() error = nil, want index 0 rejection")
	}
	if !strings.Contains(err.Error(), "index 0 is reserved") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "index 0 is reserved")
	}
}

func TestLoad_CommentsAndBlanks(t *testing.T) {
	input := `// header comment
// another comment

mapA 42
   // indented comment

mapB
mapC 100
`
	reg, err := Load(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, ok := reg.Get("mapA"); !ok || got != 42 {
		t.Errorf("Get(mapA) = (%d, %v), want (42, true)", got, ok)
	}
	if got, ok := reg.Get("mapB"); !ok || got != 43 {
		t.Errorf("Get(mapB) = (%d, %v), want (43, true)", got, ok)
	}
	if got, ok := reg.Get("mapC"); !ok || got != 100 {
		t.Errorf("Get(mapC) = (%d, %v), want (100, true)", got, ok)
	}
	if reg.Len() != 3 {
		t.Errorf("Len() = %d, want 3", reg.Len())
	}
}

func TestLoadFile_Missing(t *testing.T) {
	_, err := LoadFile(filepath.Join(t.TempDir(), "does_not_exist.txt"))
	if err == nil {
		t.Fatal("LoadFile() error = nil, want open error")
	}
	if !strings.Contains(err.Error(), "open map_index") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "open map_index")
	}
}

func TestLoad_TabSeparator(t *testing.T) {
	input := "mapA\t10\nmapB\t11\n"
	reg, err := Load(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, ok := reg.Get("mapA"); !ok || got != 10 {
		t.Errorf("Get(mapA) = (%d, %v), want (10, true)", got, ok)
	}
	if got, ok := reg.Get("mapB"); !ok || got != 11 {
		t.Errorf("Get(mapB) = (%d, %v), want (11, true)", got, ok)
	}
}
