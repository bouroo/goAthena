//go:build unit

package packet

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCZTradeRequest(t *testing.T) {
	t.Parallel()
	good := func() []byte {
		f := make([]byte, sizeCZTradeRequest)
		writeLE16(f[0:], HeaderCZTRADEREQUEST)
		writeLE32(f[2:], 0x0A0B0C0D) // target GID
		return f
	}()

	got, err := ParseCZTradeRequest(good)
	require.NoError(t, err)
	assert.Equal(t, uint32(0x0A0B0C0D), got.TargetGID)

	out, err := ParseCZTradeRequest(append([]byte{}, good...))
	require.NoError(t, err)
	assert.Equal(t, got, out)

	// Encode round-trips (for the e2e harness).
	var buf bytes.Buffer
	require.NoError(t, got.Encode(&buf))
	assert.Len(t, buf.Bytes(), sizeCZTradeRequest)
	rt, err := ParseCZTradeRequest(buf.Bytes())
	require.NoError(t, err)
	assert.Equal(t, got, rt)

	_, err = ParseCZTradeRequest(make([]byte, sizeCZTradeRequest-1))
	require.Error(t, err)

	bad := make([]byte, sizeCZTradeRequest)
	writeLE16(bad[0:], HeaderCZTRADEACK)
	_, err = ParseCZTradeRequest(bad)
	require.Error(t, err)
}

func TestParseCZTradeAck(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		typ  uint8
	}{
		{"accept", CZTradeAckAccept},
		{"cancel", CZTradeAckCancel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := make([]byte, sizeCZTradeAck)
			writeLE16(f[0:], HeaderCZTRADEACK)
			f[2] = tc.typ
			got, err := ParseCZTradeAck(f)
			require.NoError(t, err)
			assert.Equal(t, tc.typ, got.Type)
			var buf bytes.Buffer
			require.NoError(t, got.Encode(&buf))
			assert.Len(t, buf.Bytes(), sizeCZTradeAck)
		})
	}
	_, err := ParseCZTradeAck(make([]byte, sizeCZTradeAck-1))
	require.Error(t, err)
}

func TestEncodeCZTradeCancel(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	require.NoError(t, EncodeCZTradeCancel(&buf))
	require.Len(t, buf.Bytes(), sizeCZTradeCancel)
	assert.Equal(t, HeaderCZTRADECANCEL, binary.LittleEndian.Uint16(buf.Bytes()[0:2]))
}

func TestTradeRequestResponse_Encode(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	r := TradeRequestResponse{RequesterName: "Alice", TargetID: 0x12345678, TargetLv: 42}
	require.NoError(t, r.Encode(&buf))
	out := buf.Bytes()
	require.Len(t, out, sizeZCReqExchange, "ZC_REQ_EXCHANGE_ITEM fixed 32 bytes")
	assert.Equal(t, HeaderZCREQEXCHANGEITEM, binary.LittleEndian.Uint16(out[0:2]))
	// Name slot [2:26], NUL-padded after "Alice".
	assert.Equal(t, []byte("Alice"), out[2:7])
	assert.Equal(t, byte(0), out[7], "name slot NUL-padded")
	assert.Equal(t, uint32(0x12345678), binary.LittleEndian.Uint32(out[26:30]))
	assert.Equal(t, uint16(42), binary.LittleEndian.Uint16(out[30:32]))
	assert.Equal(t, sizeZCReqExchange, r.Size())

	// Overlong name truncates to NAME_LENGTH-1 (23) bytes, no overrun.
	overlong := TradeRequestResponse{RequesterName: string(bytes.Repeat([]byte("n"), 50))}
	var big bytes.Buffer
	require.NoError(t, overlong.Encode(&big))
	assert.Len(t, big.Bytes(), sizeZCReqExchange)
}

func TestTradeAckResponse_Encode(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	r := TradeAckResponse{Result: TradeAckAccept, TargetID: 0xCAFEBABE, TargetLv: 99}
	require.NoError(t, r.Encode(&buf))
	out := buf.Bytes()
	require.Len(t, out, sizeZCAckExchange, "ZC_ACK_EXCHANGE_ITEM fixed 9 bytes")
	assert.Equal(t, HeaderZCACKEXCHANGEITEM, binary.LittleEndian.Uint16(out[0:2]))
	assert.Equal(t, uint8(TradeAckAccept), out[2])
	assert.Equal(t, uint32(0xCAFEBABE), binary.LittleEndian.Uint32(out[3:7]))
	assert.Equal(t, uint16(99), binary.LittleEndian.Uint16(out[7:9]))
	assert.Equal(t, sizeZCAckExchange, r.Size())
}

func TestCancelExchangeResponse_Encode(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	r := CancelExchangeResponse{}
	require.NoError(t, r.Encode(&buf))
	out := buf.Bytes()
	require.Len(t, out, sizeZCCancelExchange, "ZC_CANCEL_EXCHANGE_ITEM fixed 2 bytes")
	assert.Equal(t, HeaderZCCANCELEXCHANGEITEM, binary.LittleEndian.Uint16(out[0:2]))
	assert.Equal(t, sizeZCCancelExchange, r.Size())
}
