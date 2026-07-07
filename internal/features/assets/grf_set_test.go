//go:build unit

package assets

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenGRFSet_MissingDir(t *testing.T) {
	t.Parallel()

	_, err := OpenGRFSet(filepath.Join(t.TempDir(), "does-not-exist"), 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "read grf dir")
}

func TestOpenGRFSet_NoGRFFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, err := OpenGRFSet(dir, 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no .grf files")
}

func TestOpenGRFSet_FiltersNonGRF(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("hi"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "data.zip"), []byte("zip"), 0o600))

	_, err := OpenGRFSet(dir, 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no .grf files")
}

func TestGRFSet_HasFile_Empty(t *testing.T) {
	t.Parallel()

	gs := &GRFSet{}
	require.False(t, gs.HasFile("any"))
}

func TestGRFSet_ReadFile_Empty(t *testing.T) {
	t.Parallel()

	gs := &GRFSet{}
	_, err := gs.ReadFile("any")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestGRFSet_Close_Empty(t *testing.T) {
	t.Parallel()

	gs := &GRFSet{}
	require.NoError(t, gs.Close())
}
