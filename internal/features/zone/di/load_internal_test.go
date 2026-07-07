//go:build unit

package di

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/pkg/ro/romap"
)

const (
	gatHeaderSkip = 6
	gatXSOFF      = 6
	gatYSOFF      = 10
	gatCellBytes  = 20
)

func buildGAT(t *testing.T, width, height int, cells []byte, corner [4]float32) []byte {
	t.Helper()

	total := gatHeaderSkip + 2*4 + width*height*gatCellBytes
	buf := make([]byte, total)
	copy(buf[0:4], "GRAT")
	buf[4] = 0x00
	buf[5] = 0x01
	binary.LittleEndian.PutUint32(buf[gatXSOFF:], uint32(width))
	binary.LittleEndian.PutUint32(buf[gatYSOFF:], uint32(height))
	off := gatHeaderSkip + 2*4
	for i := range width * height {
		binary.LittleEndian.PutUint32(buf[off+0:off+4], math.Float32bits(corner[0]))
		binary.LittleEndian.PutUint32(buf[off+4:off+8], math.Float32bits(corner[1]))
		binary.LittleEndian.PutUint32(buf[off+8:off+12], math.Float32bits(corner[2]))
		binary.LittleEndian.PutUint32(buf[off+12:off+16], math.Float32bits(corner[3]))
		ct := byte(0)
		if i < len(cells) {
			ct = cells[i]
		}
		binary.LittleEndian.PutUint32(buf[off+16:off+20], uint32(ct))
		off += gatCellBytes
	}
	return buf
}

func writeRSW(version uint16, water float32) []byte {
	// Offsets mirror pkg/ro/romap/rsw.go; kept inlined because that file is
	// in another package and its constants are unexported.
	var off int
	switch {
	case version >= 0x0205:
		off = 171
	case version >= 0x0202:
		off = 167
	default:
		off = 166
	}

	buf := make([]byte, off+4)
	copy(buf[:4], "GRSW")
	buf[4] = byte(version >> 8)
	// #nosec G115 -- rsw version numbers fit in a single byte.
	buf[5] = byte(version)
	binary.LittleEndian.PutUint32(buf[off:off+4], math.Float32bits(water))
	return buf
}

func TestLoadMap_Success(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	name := "prontera"

	width, height := 4, 3
	// Mix walkable (0), wall (1), and walkable water (3) cells.
	cells := []byte{
		0, 0, 1, 0,
		1, 0, 0, 3,
		0, 0, 0, 1,
	}
	gat := buildGAT(t, width, height, cells, [4]float32{10, 10, 10, 10})
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".gat"), gat, 0o600))

	const water = 5.0
	rsw := writeRSW(0x0202, water)
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".rsw"), rsw, 0o600))

	md, err := loadMap(dir, name)
	require.NoError(t, err)
	require.NotNil(t, md)

	assert.Equal(t, name, md.Name)
	assert.Equal(t, width, md.Width)
	assert.Equal(t, height, md.Height)
	assert.Equal(t, width*height, len(md.Walkable))
	assert.Equal(t, width*height, len(md.Heights))

	expectedWalkable := []bool{
		true, true, false, true,
		false, true, true, true,
		true, true, true, false,
	}
	assert.Equal(t, expectedWalkable, md.Walkable)

	// romap truncates float→int32 then back to float32; integral values
	// round-trip exactly.
	assert.Equal(t, float32(5), md.WaterLevel)

	for i := range md.Heights {
		assert.InDelta(t, float32(10), md.Heights[i], 1e-6)
	}
}

func TestLoadMap_MissingGAT(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	md, err := loadMap(dir, "missing")
	require.Error(t, err)
	assert.Nil(t, md)
	assert.Contains(t, err.Error(), "read")
	assert.Contains(t, err.Error(), "missing.gat")
}

func TestLoadMap_MissingRSW_SoftFail(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	name := "norwater"

	gat := buildGAT(t, 2, 2, []byte{0, 0, 0, 0}, [4]float32{0, 0, 0, 0})
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".gat"), gat, 0o600))

	md, err := loadMap(dir, name)
	require.NoError(t, err)
	require.NotNil(t, md)

	assert.Equal(t, name, md.Name)
	assert.Equal(t, 2, md.Width)
	assert.Equal(t, 2, md.Height)
	assert.Equal(t, float32(romap.WaterAbsent), md.WaterLevel)
}

func TestLoadMap_MalformedGAT(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	name := "corrupt"

	// Too short to even hold the header — parseGAT returns io.ErrUnexpectedEOF.
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".gat"), []byte{0xFF, 0xFE, 0xFD}, 0o600))

	md, err := loadMap(dir, name)
	require.Error(t, err)
	assert.Nil(t, md)
	assert.Contains(t, err.Error(), "parse")
	assert.Contains(t, err.Error(), name+".gat")
}
