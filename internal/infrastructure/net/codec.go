package net

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/bouroo/goAthena/pkg/ro/crypto"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// ErrIncomplete is returned by Next when the buffer does not yet contain
// a complete packet. The caller should Feed more bytes and retry.
var ErrIncomplete = errors.New("packet incomplete: need more data")

// ErrUnknownPacket is returned by Next when the decoded command ID is not
// registered in the packet database. The caller should disconnect the
// client (matching rathena/src/map/clif.cpp:25718-25744).
var ErrUnknownPacket = errors.New("unknown packet command")

// MinVariableLength is the smallest acceptable wire length for a
// variable-length packet. A sane kRO variable-length packet must at least
// contain [cmd:2][length:2]; anything smaller is malformed.
//
// Mirrors the `4 <= len <= 32768` check at rathena/src/map/clif.cpp:25751.
const MinVariableLength = 4

// MaxFrameSize caps variable-length wire frames at 32 KiB, matching the
// rathena/src/map/clif.cpp:25751 sanity check (4 <= len <= 32768).
const MaxFrameSize = 32 * 1024

// headerSize is the number of bytes needed to determine the frame size
// of a variable-length packet: [cmd:2][wireLength:2].
const headerSize = MinVariableLength

// mode selects between plaintext (login/char) and obfuscated (map) parsing.
type mode int

const (
	modeLogin mode = iota
	modeMap
)

// Decoder frames a kRO TCP byte stream into discrete packets.
//
// It is not safe for concurrent use — a Decoder is owned by a single
// connection's read path. Each connection must construct its own Decoder.
type Decoder struct {
	db        *packet.DB
	mode      mode
	keys      [3]uint32 // per-PACKETVER triplet for map mode (preserved across first-packet decode)
	obf       *crypto.Obfuscator
	firstDone bool
	buf       []byte
}

// NewLoginDecoder creates a plaintext decoder for login/char-server traffic.
// All command IDs are passed through unchanged; framing (fixed vs variable
// length) still uses the supplied packet database.
func NewLoginDecoder(db *packet.DB) *Decoder {
	return &Decoder{db: db, mode: modeLogin}
}

// NewMapDecoder creates a deobfuscating decoder for map-server traffic.
// key0, key1, key2 are the per-PACKETVER triplet from crypto.KeysForVersion.
//
// When all three keys are zero (PACKETVER > 20180307) the decoder is the
// identity transform for command IDs but still uses map-server framing
// semantics. The session Obfuscator is constructed lazily on the first
// successful packet decode so a false-start never advances state.
func NewMapDecoder(db *packet.DB, key0, key1, key2 uint32) *Decoder {
	return &Decoder{
		db:   db,
		mode: modeMap,
		keys: [3]uint32{key0, key1, key2},
	}
}

// Feed appends raw bytes to the decoder's internal buffer. The input slice
// is copied — the caller may reuse it immediately after the call.
//
// Decoder does not bound total buffer size; the transport layer should
// impose its own stall/per-message ceilings (see conf/packet_athena.conf in
// rAthena — DDoS and stall-time knobs) to prevent unbounded growth from
// malicious peers.
func (d *Decoder) Feed(p []byte) {
	d.buf = append(d.buf, p...)
}

// Buffered returns the number of unread bytes accumulated by Feed and not
// yet consumed by Next. It includes the bytes currently being parsed for
// an in-flight Next call.
func (d *Decoder) Buffered() int {
	return len(d.buf)
}

// decodeCmd returns the decoded cmd id for a raw 2-byte big-endian /
// little-endian read of buf[0:2], using the decoder's mode and state.
// Extracted from Next to keep its cyclomatic complexity <= 15.
func (d *Decoder) decodeCmd(rawCmd uint16) uint16 {
	switch d.mode {
	case modeLogin:
		return rawCmd
	case modeMap:
		switch {
		case !d.firstDone:
			return crypto.FirstPacketDecode(d.keys[0], d.keys[1], d.keys[2], rawCmd)
		case d.obf != nil:
			return d.obf.Decode(rawCmd)
		default:
			return rawCmd
		}
	}
	return rawCmd
}

// frameSize resolves the on-wire size for cmd from the packet DB and the
// buffered bytes. Returns (size, err). err is ErrIncomplete when more data
// is needed; any other error indicates a malformed packet.
func (d *Decoder) frameSize(cmd uint16, length int) (int, error) {
	switch {
	case length == packet.VariableLength:
		if len(d.buf) < headerSize {
			return 0, ErrIncomplete
		}
		wireLen := binary.LittleEndian.Uint16(d.buf[2:4])
		if wireLen < MinVariableLength || int(wireLen) > MaxFrameSize {
			return 0, fmt.Errorf("invalid variable-length packet size: %d", wireLen)
		}
		if len(d.buf) < int(wireLen) {
			return 0, ErrIncomplete
		}
		return int(wireLen), nil
	case length > 0:
		if len(d.buf) < length {
			return 0, ErrIncomplete
		}
		return length, nil
	default:
		return 0, fmt.Errorf("invalid packet length %d for cmd 0x%04x", length, cmd)
	}
}

// Next extracts the next complete packet from the buffer.
//
// Returns the decoded command ID and the full packet frame with the decoded
// command ID patched into bytes [0:2]. The returned frame is a fresh copy
// and is safe to retain past subsequent Next/Feed calls. For map mode, the
// patched cmd matches rathena/src/map/clif.cpp:25773, so the in-place
// rewrite matches rAthena's behaviour exactly.
//
// Errors:
//   - ErrIncomplete: buffer needs more data.
//   - ErrUnknownPacket: decoded cmd is not in the database — disconnect.
//   - other: malformed wire length (not in the [MinVariableLength, MaxFrameSize] range).
func (d *Decoder) Next() (cmd uint16, frame []byte, err error) {
	if len(d.buf) < 2 {
		return 0, nil, ErrIncomplete
	}

	rawCmd := binary.LittleEndian.Uint16(d.buf[0:2])
	cmd = d.decodeCmd(rawCmd)

	length, ok := d.db.Length(cmd)
	if !ok {
		return 0, nil, fmt.Errorf("%w: 0x%04x", ErrUnknownPacket, cmd)
	}

	size, err := d.frameSize(cmd, length)
	if err != nil {
		return 0, nil, err
	}

	frame = make([]byte, size)
	copy(frame, d.buf[:size])
	binary.LittleEndian.PutUint16(frame[0:2], cmd)
	d.buf = d.buf[size:]

	if d.mode == modeMap && !d.firstDone {
		d.firstDone = true
		d.obf = crypto.NewObfuscator(d.keys[0], d.keys[1], d.keys[2])
	}

	return cmd, frame, nil
}
