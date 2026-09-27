package domain

import (
	"errors"
	"testing"
)

func TestLocalDirectory_ResolvesLocalMarker(t *testing.T) {
	dir := NewLocalDirectory()
	for _, mapName := range []string{"prontera", "new_1-1", ""} {
		z, err := dir.Resolve(mapName)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", mapName, err)
		}
		if z != (Zone{}) {
			t.Errorf("Resolve(%q) = %+v, want zero Zone (local marker)", mapName, z)
		}
	}
}

func TestMapDirectory_ErrUnknownMap(t *testing.T) {
	if !errors.Is(ErrUnknownMap, ErrUnknownMap) {
		t.Fatal("sentinel must exist")
	}
	// Compile-time: LocalDirectory satisfies the port.
	var _ MapDirectory = LocalDirectory{}
}
