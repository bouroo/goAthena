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

func TestParseCZAddExchangeItem(t *testing.T) {
	t.Parallel()
	good := func() []byte {
		f := make([]byte, sizeCZAddExchangeItem)
		writeLE16(f[0:], HeaderCZADDEXCHANGEITEM)
		writeLE16(f[2:], 5)  // item at bag slot 5
		writeLE32(f[4:], 10) // amount
		return f
	}()
	got, err := ParseCZAddExchangeItem(good)
	require.NoError(t, err)
	assert.Equal(t, uint16(5), got.Index)
	assert.Equal(t, int32(10), got.Amount)
	// Encode round-trip.
	var buf bytes.Buffer
	require.NoError(t, got.Encode(&buf))
	assert.Len(t, buf.Bytes(), sizeCZAddExchangeItem)
	rt, err := ParseCZAddExchangeItem(buf.Bytes())
	require.NoError(t, err)
	assert.Equal(t, got, rt)
	// Short frame errors.
	_, err = ParseCZAddExchangeItem(make([]byte, sizeCZAddExchangeItem-1))
	require.Error(t, err)
}

func TestZCAddExchangeItem_EncodeLayout(t *testing.T) {
	t.Parallel()
	r := ZCAddExchangeItem{
		ItemID: 0x01020304, ItemType: 5, Amount: 7, Identified: 1, Damaged: 0,
		Cards:    [4]uint32{0xAA, 0xBB, 0xCC, 0xDD},
		Options:  [5]ItemOption{{Index: 1, Value: 5, Param: 2}, {Index: 3, Value: 10, Param: 0}},
		Location: 0x00010000, Look: 42, Refine: 8, Grade: 3,
	}
	var buf bytes.Buffer
	require.NoError(t, r.Encode(&buf))
	out := buf.Bytes()
	require.Len(t, out, sizeZCAddExchangeItem, "ZC_ADD fixed 62 bytes at 20250604")
	assert.Equal(t, HeaderZCADDEXCHANGEITEM, binary.LittleEndian.Uint16(out[0:2]))
	assert.Equal(t, uint32(0x01020304), binary.LittleEndian.Uint32(out[2:6]), "itemId@2")
	assert.Equal(t, uint8(5), out[6], "itemType@6")
	assert.Equal(t, int32(7), int32(binary.LittleEndian.Uint32(out[7:11])), "amount@7")
	assert.Equal(t, uint8(1), out[11], "identified@11")
	assert.Equal(t, uint8(0), out[12], "damaged@12")
	// Cards [13:29] (4×uint32).
	assert.Equal(t, uint32(0xAA), binary.LittleEndian.Uint32(out[13:17]), "card0@13")
	assert.Equal(t, uint32(0xDD), binary.LittleEndian.Uint32(out[25:29]), "card3@25")
	// Options [29:54] (5×5). opt0 index@29, value@31, param@33.
	assert.Equal(t, uint16(1), binary.LittleEndian.Uint16(out[29:31]), "opt0 index@29")
	assert.Equal(t, uint16(5), binary.LittleEndian.Uint16(out[31:33]), "opt0 value@31")
	assert.Equal(t, uint8(2), out[33], "opt0 param@33")
	// location@54, look@58, refine@60, grade@61 (post-cards at this gate).
	assert.Equal(t, uint32(0x00010000), binary.LittleEndian.Uint32(out[54:58]), "location@54")
	assert.Equal(t, uint16(42), binary.LittleEndian.Uint16(out[58:60]), "look@58")
	assert.Equal(t, uint8(8), out[60], "refine@60")
	assert.Equal(t, uint8(3), out[61], "grade@61")
	assert.Equal(t, sizeZCAddExchangeItem, r.Size())
}

func TestAckAddExchangeItem_Encode(t *testing.T) {
	t.Parallel()
	r := AckAddExchangeItem{Index: 5, Result: TradeItemAddSuccess}
	var buf bytes.Buffer
	require.NoError(t, r.Encode(&buf))
	out := buf.Bytes()
	require.Len(t, out, sizeZCAckAddExchange, "ZC_ACK_ADD fixed 5 bytes")
	assert.Equal(t, HeaderZCACKADDEXCHANGEITEM, binary.LittleEndian.Uint16(out[0:2]))
	assert.Equal(t, uint16(5), binary.LittleEndian.Uint16(out[2:4]))
	assert.Equal(t, uint8(TradeItemAddSuccess), out[4])
	assert.Equal(t, sizeZCAckAddExchange, r.Size())
}

func TestConcludeExchangeItem_Encode(t *testing.T) {
	t.Parallel()
	for _, who := range []uint8{0, 1} {
		var buf bytes.Buffer
		require.NoError(t, (ConcludeExchangeItem{Who: who}).Encode(&buf))
		out := buf.Bytes()
		require.Len(t, out, sizeZCConcludeExchange, "ZC_CONCLUDE fixed 3 bytes")
		assert.Equal(t, HeaderZCCONCLUDEEXCHANGEITEM, binary.LittleEndian.Uint16(out[0:2]))
		assert.Equal(t, who, out[2])
	}
	assert.Equal(t, sizeZCConcludeExchange, (ConcludeExchangeItem{}).Size())
}
