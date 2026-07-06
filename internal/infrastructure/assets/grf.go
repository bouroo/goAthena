package assets

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	grfMagic       = "Master of Magic"
	grfHeaderSize  = 0x2e
	grfFileBase    = 0x2e
	grfVersion01xx = 0x01
	grfVersion02xx = 0x02

	fileFlagFile          = 0x01
	fileFlagEncryptMixed  = 0x02
	fileFlagEncryptHeader = 0x04

	desBlocksHeader = 20
)

var (
	errInvalidMagic = errors.New("grf: invalid magic; expected \"Master of Magic\"")
	errUnsupported  = errors.New("grf: unsupported version")
	errNotFound     = errors.New("grf: file not found")
)

type grfEntry struct {
	name           string
	compressedSize uint32
	rawSizeAligned uint32
	rawSize        uint32
	flags          uint32
	offset         uint32
}

// GRF is a reader for a single Gravity GRF archive.
//
// A GRF is opened once and shared across goroutines; per-file reads use
// the underlying *os.File's safe ReadAt.
type GRF struct {
	file      *os.File
	fileCount uint32
	entries   map[string]*grfEntry
}

// Open opens the GRF archive at path and reads its file table into memory.
func Open(path string) (*GRF, error) {
	f, err := os.Open(path) // #nosec G304 -- caller-controlled asset path
	if err != nil {
		return nil, fmt.Errorf("grf: open %q: %w", path, err)
	}

	g := &GRF{
		file:    f,
		entries: make(map[string]*grfEntry),
	}
	if err := g.parse(); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("grf: parse %q: %w", path, err)
	}
	return g, nil
}

// Close releases the underlying file. Calling Close more than once is safe.
func (g *GRF) Close() error {
	if g.file == nil {
		return nil
	}
	err := g.file.Close()
	g.file = nil
	if err != nil {
		return fmt.Errorf("grf: close: %w", err)
	}
	return nil
}

// List returns the names of all files in the archive (sorted-by-insertion order is not guaranteed).
func (g *GRF) List() []string {
	out := make([]string, 0, len(g.entries))
	for k := range g.entries {
		out = append(out, k)
	}
	return out
}

// HasFile reports whether name exists in the archive (case-insensitive).
func (g *GRF) HasFile(name string) bool {
	_, ok := g.entries[normaliseName(name)]
	return ok
}

// ReadFile decompresses and returns the contents of name.
func (g *GRF) ReadFile(name string) ([]byte, error) {
	entry, ok := g.entries[normaliseName(name)]
	if !ok {
		return nil, fmt.Errorf("grf: read %q: %w", name, errNotFound)
	}
	return g.readEntry(entry)
}

func (g *GRF) parse() error {
	var hdr [grfHeaderSize]byte
	if _, err := io.ReadFull(g.file, hdr[:]); err != nil {
		return fmt.Errorf("read header: %w", err)
	}

	if string(hdr[:15]) != grfMagic {
		return errInvalidMagic
	}

	allow2GB := binary.LittleEndian.Uint32(hdr[0x1e:])
	if _, err := g.file.Seek(int64(allow2GB), io.SeekCurrent); err != nil {
		return fmt.Errorf("seek past header: %w", err)
	}

	version := binary.LittleEndian.Uint32(hdr[0x2a:]) >> 8
	switch version {
	case grfVersion01xx:
		g.fileCount = binary.LittleEndian.Uint32(hdr[0x26:]) - binary.LittleEndian.Uint32(hdr[0x22:]) - 7
		return g.parse01xx()
	case grfVersion02xx:
		g.fileCount = binary.LittleEndian.Uint32(hdr[0x26:]) - 7
		return g.parse02xx()
	default:
		return fmt.Errorf("version 0x%02x: %w", version, errUnsupported)
	}
}

func (g *GRF) parse01xx() error {
	listSize, err := g.remainingBytes()
	if err != nil {
		return fmt.Errorf("read 01xx list size: %w", err)
	}

	raw := make([]byte, listSize)
	if _, err := io.ReadFull(g.file, raw); err != nil {
		return fmt.Errorf("read 01xx list: %w", err)
	}

	ofs := 0
	for i := uint32(0); i < g.fileCount && ofs+6 < len(raw); i++ {
		nameLen := int(binary.LittleEndian.Uint32(raw[ofs:]))
		if nameLen < 6 || ofs+nameLen+4 > len(raw) {
			break
		}

		encName := append([]byte(nil), raw[ofs+6:ofs+nameLen]...)
		decName := decodeFilename(encName)

		ofs2 := ofs + nameLen + 4
		if ofs2+17 > len(raw) {
			break
		}

		typ := raw[ofs2+12]
		if typ&fileFlagFile == 0 {
			ofs = ofs2 + 17
			continue
		}

		srclen := binary.LittleEndian.Uint32(raw[ofs2:]) - binary.LittleEndian.Uint32(raw[ofs2+8:]) - 715
		srclenAligned := binary.LittleEndian.Uint32(raw[ofs2+4:]) - 37579
		declen := binary.LittleEndian.Uint32(raw[ofs2+8:])
		srcpos := binary.LittleEndian.Uint32(raw[ofs2+13:]) + grfFileBase

		entryType := typ
		if isFullEncrypt(decName) {
			entryType |= fileFlagEncryptMixed
		} else {
			entryType |= fileFlagEncryptHeader
		}

		g.entries[normaliseName(decName)] = &grfEntry{
			name:           decName,
			compressedSize: srclen,
			rawSizeAligned: srclenAligned,
			rawSize:        declen,
			flags:          uint32(entryType),
			offset:         srcpos,
		}
		ofs = ofs2 + 17
	}
	return nil
}

func (g *GRF) parse02xx() error {
	var eheader [8]byte
	if _, err := io.ReadFull(g.file, eheader[:]); err != nil {
		return fmt.Errorf("read eheader: %w", err)
	}

	rSize := binary.LittleEndian.Uint32(eheader[:])
	eSize := binary.LittleEndian.Uint32(eheader[4:])

	stat, err := g.file.Stat()
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	cur, err := g.file.Seek(0, io.SeekCurrent)
	if err != nil {
		return fmt.Errorf("seek: %w", err)
	}
	if int64(rSize) > stat.Size()-cur {
		return errors.New("illegal compressed file table size")
	}

	compressed := make([]byte, rSize)
	if _, err := io.ReadFull(g.file, compressed); err != nil {
		return fmt.Errorf("read compressed table: %w", err)
	}

	zr, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return fmt.Errorf("zlib reader: %w", err)
	}
	defer func() { _ = zr.Close() }()

	table := make([]byte, eSize)
	if _, err := io.ReadFull(zr, table); err != nil {
		return fmt.Errorf("decompress table: %w", err)
	}

	ofs := 0
	for i := uint32(0); i < g.fileCount && ofs < len(table); i++ {
		nameEnd := bytes.IndexByte(table[ofs:], 0)
		if nameEnd < 0 {
			break
		}
		name := string(table[ofs : ofs+nameEnd])
		ofs2 := ofs + nameEnd + 1

		if ofs2+17 > len(table) {
			break
		}

		typ := table[ofs2+12]
		if typ&fileFlagFile != 0 {
			srclen := binary.LittleEndian.Uint32(table[ofs2:])
			srclenAligned := binary.LittleEndian.Uint32(table[ofs2+4:])
			declen := binary.LittleEndian.Uint32(table[ofs2+8:])
			srcpos := binary.LittleEndian.Uint32(table[ofs2+13:]) + grfFileBase

			g.entries[normaliseName(name)] = &grfEntry{
				name:           name,
				compressedSize: srclen,
				rawSizeAligned: srclenAligned,
				rawSize:        declen,
				flags:          uint32(typ),
				offset:         srcpos,
			}
		}
		ofs = ofs2 + 17
	}
	return nil
}

func (g *GRF) readEntry(e *grfEntry) ([]byte, error) {
	bufSize := e.rawSizeAligned
	if bufSize == 0 {
		bufSize = e.compressedSize
	}
	buf := make([]byte, bufSize)
	if _, err := g.file.ReadAt(buf, int64(e.offset)); err != nil {
		return nil, fmt.Errorf("grf: read %q at %d: %w", e.name, e.offset, err)
	}

	if err := grfDecode(buf, e.flags, int(e.compressedSize)); err != nil {
		return nil, fmt.Errorf("grf: decode %q: %w", e.name, err)
	}

	zr, err := zlib.NewReader(bytes.NewReader(buf[:e.compressedSize]))
	if err != nil {
		return nil, fmt.Errorf("grf: zlib reader %q: %w", e.name, err)
	}
	defer func() { _ = zr.Close() }()

	out := make([]byte, e.rawSize)
	if _, err := io.ReadFull(zr, out); err != nil {
		return nil, fmt.Errorf("grf: decompress %q: %w", e.name, err)
	}
	return out, nil
}

func (g *GRF) remainingBytes() (int64, error) {
	stat, err := g.file.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat: %w", err)
	}
	cur, err := g.file.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, fmt.Errorf("seek: %w", err)
	}
	return stat.Size() - cur, nil
}

func grfDecode(buf []byte, flags uint32, srclen int) error {
	if flags&fileFlagEncryptMixed != 0 {
		return errors.New("encrypted (mixed/mode 0) entries not supported")
	}
	if flags&fileFlagEncryptHeader != 0 {
		desDecryptHeader(buf[:srclen])
	}
	return nil
}

func normaliseName(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, "\\", "/"))
}
