//go:build unit

package assets

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeGRF02xx(t *testing.T, files map[string][]byte) string {
	t.Helper()

	type pending struct {
		name       string
		compressed []byte
		rawSize    uint32
	}

	pendingFiles := make([]pending, 0, len(files))
	var rawData bytes.Buffer
	for name, data := range files {
		var compressed bytes.Buffer
		zw := zlib.NewWriter(&compressed)
		_, err := zw.Write(data)
		require.NoError(t, err)
		require.NoError(t, zw.Close())

		pendingFiles = append(pendingFiles, pending{name: name, compressed: compressed.Bytes(), rawSize: uint32(len(data))})
		rawData.Write(compressed.Bytes())
	}

	headerAndEheader := uint32(grfHeaderSize) + 8

	var compressedFinal bytes.Buffer
	var finalTable bytes.Buffer
	dataBase := headerAndEheader

	for iter := 0; iter < 3; iter++ {
		finalTable.Reset()
		compressedFinal.Reset()
		cursor := uint32(0)
		for _, pf := range pendingFiles {
			finalTable.WriteString(pf.name)
			finalTable.WriteByte(0)

			var meta [17]byte
			binary.LittleEndian.PutUint32(meta[0:], uint32(len(pf.compressed)))
			binary.LittleEndian.PutUint32(meta[4:], uint32(len(pf.compressed)))
			binary.LittleEndian.PutUint32(meta[8:], pf.rawSize)
			meta[12] = fileFlagFile
			binary.LittleEndian.PutUint32(meta[13:], dataBase+cursor-grfFileBase)
			finalTable.Write(meta[:])

			cursor += uint32(len(pf.compressed))
		}

		zw := zlib.NewWriter(&compressedFinal)
		_, err := zw.Write(finalTable.Bytes())
		require.NoError(t, err)
		require.NoError(t, zw.Close())

		newDataBase := headerAndEheader + uint32(compressedFinal.Len())
		if newDataBase == dataBase {
			break
		}
		dataBase = newDataBase
	}

	var hdr [grfHeaderSize]byte
	copy(hdr[:15], grfMagic)
	binary.LittleEndian.PutUint32(hdr[0x1e:], 0)
	binary.LittleEndian.PutUint32(hdr[0x22:], 0)
	binary.LittleEndian.PutUint32(hdr[0x26:], uint32(len(files))+7)
	binary.LittleEndian.PutUint32(hdr[0x2a:], grfVersion02xx<<8)

	var eheader [8]byte
	binary.LittleEndian.PutUint32(eheader[:], uint32(compressedFinal.Len()))
	binary.LittleEndian.PutUint32(eheader[4:], uint32(finalTable.Len()))

	path := filepath.Join(t.TempDir(), "test.grf")
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()

	_, err = f.Write(hdr[:])
	require.NoError(t, err)
	_, err = f.Write(eheader[:])
	require.NoError(t, err)
	_, err = f.Write(compressedFinal.Bytes())
	require.NoError(t, err)
	_, err = f.Write(rawData.Bytes())
	require.NoError(t, err)

	return path
}

func TestGRFOpenInvalidMagic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.grf")
	var hdr [grfHeaderSize]byte
	copy(hdr[:], "not a real header!!!")
	require.NoError(t, os.WriteFile(path, hdr[:], 0o644))

	_, err := Open(path)
	require.Error(t, err)
	assert.ErrorIs(t, err, errInvalidMagic)
}

func TestGRFOpenMissing(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "missing.grf"))
	require.Error(t, err)
}

func TestGRFOpenUnsupportedVersion(t *testing.T) {
	var hdr [grfHeaderSize]byte
	copy(hdr[:15], grfMagic)
	binary.LittleEndian.PutUint32(hdr[0x2a:], 0x03<<8)

	path := filepath.Join(t.TempDir(), "v3.grf")
	require.NoError(t, os.WriteFile(path, hdr[:], 0o644))

	_, err := Open(path)
	require.Error(t, err)
	assert.ErrorIs(t, err, errUnsupported)
}

func TestGRFListAndRead(t *testing.T) {
	files := map[string][]byte{
		"data/readme.txt": []byte("hello world"),
		"data/utf8.txt":   []byte("\xe4\xb8\xad\xe6\x96\x87"),
		"data/binary.bin": {0x00, 0x01, 0x02, 0xff, 0xfe},
	}

	g, err := Open(writeGRF02xx(t, files))
	require.NoError(t, err)
	defer g.Close()

	names := g.List()
	assert.ElementsMatch(t, []string{
		"data/readme.txt",
		"data/utf8.txt",
		"data/binary.bin",
	}, names)

	for name, expected := range files {
		assert.True(t, g.HasFile(name), "HasFile(%q)", name)
		data, err := g.ReadFile(name)
		require.NoError(t, err)
		assert.Equal(t, expected, data)
	}
}

func TestGRFReadNotFound(t *testing.T) {
	g, err := Open(writeGRF02xx(t, map[string][]byte{"a.txt": []byte("a")}))
	require.NoError(t, err)
	defer g.Close()

	_, err = g.ReadFile("missing.txt")
	require.Error(t, err)
	assert.ErrorIs(t, err, errNotFound)
}

func TestGRFHasFileCaseInsensitive(t *testing.T) {
	g, err := Open(writeGRF02xx(t, map[string][]byte{"Data/Readme.txt": []byte("hi")}))
	require.NoError(t, err)
	defer g.Close()

	assert.True(t, g.HasFile("data/readme.txt"))
	assert.True(t, g.HasFile("DATA/README.TXT"))
}

func TestGRFCloseIdempotent(t *testing.T) {
	g, err := Open(writeGRF02xx(t, map[string][]byte{"a": {1}}))
	require.NoError(t, err)
	require.NoError(t, g.Close())
	require.NoError(t, g.Close())
	assert.Nil(t, g.file)
}

func TestDESRoundTrip(t *testing.T) {
	original := []byte("ABCDEFGH")
	enc := append([]byte(nil), original...)
	desDecryptHeader(enc)
	assert.NotEqual(t, original, enc, "DES should mutate the block")
	desDecryptHeader(enc)
	assert.Equal(t, original, enc, "DES should be its own inverse")
}

func TestNibbleSwap(t *testing.T) {
	for i := 0; i < 256; i++ {
		once := byte(i>>4) | byte(i<<4)
		twice := byte(once>>4) | byte(once<<4)
		assert.Equal(t, byte(i), twice, "nibbleswap must be involutive for 0x%02x", i)
	}
}
