//go:build unit

package app_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// encodeWhisperReq builds a CZ_WHISPER frame for a given target nick + message,
// mirroring what a real client sends.
func encodeWhisperReq(t *testing.T, nick, msg string) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (packet.CZWhisperRequest{TargetNick: nick, Message: msg}).Encode(&buf))
	return buf.Bytes()
}

// TestWhisperHandler_DeliversToRecipient asserts the core happy path: a sender
// whispers a registered recipient by nick → the recipient receives ZC_WHISPER
// (0x09de) with the sender's GID + name + message, and the sender receives
// ZC_ACK_WHISPER (0x09df) result=0 (success), CID=sender char_id.
func TestWhisperHandler_DeliversToRecipient(t *testing.T) {
	t.Parallel()
	registry := domain.NewPlayerRegistry()
	const (
		senderAID                  uint32 = 2000900
		senderCID                  uint32 = 150004
		recipientAID, recipientCID uint32 = 2000901, 150005
	)
	senderConn := &captureConn{role: gwdomain.RoleMap, auth: gwdomain.ConnAuth{AccountID: senderAID}}
	recipientConn := &captureConn{role: gwdomain.RoleMap}
	register := func(conn gwdomain.Conn, aid, cid uint32, name string) {
		require.NoError(t, registry.Register(&domain.Player{
			Conn: conn, EntityID: aoi.EntityID(aid), AccountID: aid, CharID: cid, MapName: "prontera", Name: name,
		}))
	}
	register(senderConn, senderAID, senderCID, "Sender")
	register(recipientConn, recipientAID, recipientCID, "Recipient")

	h := app.NewWhisperHandler(registry)
	err := h.Handle(context.Background(), senderConn, gwdomain.Frame{
		Cmd: packet.HeaderCZWHISPER,
		Raw: encodeWhisperReq(t, "Recipient", "hello there"),
	})
	require.NoError(t, err)

	// Recipient received ZC_WHISPER.
	recv := recipientConn.buf.Bytes()
	require.GreaterOrEqual(t, len(recv), 11, "recipient got a ZC_WHISPER frame")
	assert.Equal(t, uint16(packet.HeaderZCWHISPER), binary.LittleEndian.Uint16(recv[0:2]),
		"recipient frame opcode = 0x09de")
	assert.Equal(t, senderAID, binary.LittleEndian.Uint32(recv[4:8]),
		"ZC_WHISPER senderGID = sender account_id")
	assert.Equal(t, "Sender", string(bytes.TrimRight(recv[8:32], "\x00")),
		"ZC_WHISPER sender name = 'Sender'")
	assert.Equal(t, uint8(0), recv[32], "ZC_WHISPER isAdmin = 0")
	assert.Equal(t, "hello there", string(bytes.TrimRight(recv[33:], "\x00")),
		"ZC_WHISPER message body")

	// Sender received ZC_ACK_WHISPER success.
	ack := senderConn.buf.Bytes()
	require.Len(t, ack, 7, "sender got exactly the 7-byte ZC_ACK_WHISPER")
	assert.Equal(t, uint16(packet.HeaderZCACKWHISPER), binary.LittleEndian.Uint16(ack[0:2]),
		"sender ack opcode = 0x09df")
	assert.Equal(t, uint8(0), ack[2], "ZC_ACK_WHISPER result = 0 (success)")
	assert.Equal(t, senderCID, binary.LittleEndian.Uint32(ack[3:7]),
		"ZC_ACK_WHISPER CID = sender char_id")
}

// TestWhisperHandler_TargetOffline asserts an absent recipient yields the
// offline ack (result=1) and no delivery attempt.
func TestWhisperHandler_TargetOffline(t *testing.T) {
	t.Parallel()
	registry := domain.NewPlayerRegistry()
	senderConn := &captureConn{role: gwdomain.RoleMap, auth: gwdomain.ConnAuth{AccountID: 2000902}}
	require.NoError(t, registry.Register(&domain.Player{
		Conn: senderConn, EntityID: aoi.EntityID(2000902), AccountID: 2000902, CharID: 150006, MapName: "prontera", Name: "Sender2",
	}))

	h := app.NewWhisperHandler(registry)
	err := h.Handle(context.Background(), senderConn, gwdomain.Frame{
		Cmd: packet.HeaderCZWHISPER,
		Raw: encodeWhisperReq(t, "Nobody", "ping"),
	})
	require.NoError(t, err)

	ack := senderConn.buf.Bytes()
	require.Len(t, ack, 7, "offline recipient → sender gets only the ack")
	assert.Equal(t, uint8(1), ack[2], "ZC_ACK_WHISPER result = 1 (target offline)")
}

// TestWhisperHandler_CaseInsensitiveTarget asserts nick resolution is
// case-insensitive (rAthena map_nick2sd strncasecmp).
func TestWhisperHandler_CaseInsensitiveTarget(t *testing.T) {
	t.Parallel()
	registry := domain.NewPlayerRegistry()
	recipientConn := &captureConn{role: gwdomain.RoleMap}
	require.NoError(t, registry.Register(&domain.Player{
		Conn: recipientConn, EntityID: aoi.EntityID(2000903), AccountID: 2000903, CharID: 150007, MapName: "prontera", Name: "Alice",
	}))
	senderConn := &captureConn{role: gwdomain.RoleMap, auth: gwdomain.ConnAuth{AccountID: 2000904}}
	require.NoError(t, registry.Register(&domain.Player{
		Conn: senderConn, EntityID: aoi.EntityID(2000904), AccountID: 2000904, CharID: 150008, MapName: "prontera", Name: "Bob",
	}))

	h := app.NewWhisperHandler(registry)
	err := h.Handle(context.Background(), senderConn, gwdomain.Frame{
		Cmd: packet.HeaderCZWHISPER,
		Raw: encodeWhisperReq(t, "aLiCe", "case test"),
	})
	require.NoError(t, err)
	recv := recipientConn.buf.Bytes()
	require.GreaterOrEqual(t, len(recv), 11, "recipient got the whisper despite case mismatch")
	assert.Equal(t, "case test", string(bytes.TrimRight(recv[33:], "\x00")))
}

// TestWhisperHandler_NoAuth asserts the impersonation guard: a CZ_WHISPER on a
// connection whose CZ_ENTER did not complete is dropped silently.
func TestWhisperHandler_NoAuth(t *testing.T) {
	t.Parallel()
	registry := domain.NewPlayerRegistry()
	h := app.NewWhisperHandler(registry)
	conn := &captureConn{role: gwdomain.RoleMap} // no auth cached

	err := h.Handle(context.Background(), conn, gwdomain.Frame{
		Cmd: packet.HeaderCZWHISPER,
		Raw: encodeWhisperReq(t, "Anyone", "ghost"),
	})
	assert.NoError(t, err, "unverified session is a silent drop, not an error")
	assert.Empty(t, conn.buf.Bytes(), "nothing written on a silent drop")
}

// TestWhisperHandler_NotRegistered asserts a late packet after the sender's
// disconnect is dropped silently.
func TestWhisperHandler_NotRegistered(t *testing.T) {
	t.Parallel()
	registry := domain.NewPlayerRegistry()
	h := app.NewWhisperHandler(registry)
	conn := &captureConn{role: gwdomain.RoleMap, auth: gwdomain.ConnAuth{AccountID: 999999}}

	err := h.Handle(context.Background(), conn, gwdomain.Frame{
		Cmd: packet.HeaderCZWHISPER,
		Raw: encodeWhisperReq(t, "Anyone", "late"),
	})
	assert.NoError(t, err, "late packet after disconnect is a silent drop")
	assert.Empty(t, conn.buf.Bytes(), "nothing written for an unknown sender")
}

// TestWhisperHandler_ParseError asserts a malformed frame is a wrapped error
// (gateway logs it, conn stays open).
func TestWhisperHandler_ParseError(t *testing.T) {
	t.Parallel()
	registry := domain.NewPlayerRegistry()
	h := app.NewWhisperHandler(registry)
	conn := &captureConn{role: gwdomain.RoleMap, auth: gwdomain.ConnAuth{AccountID: 1}}

	err := h.Handle(context.Background(), conn, gwdomain.Frame{
		Cmd: packet.HeaderCZWHISPER,
		Raw: []byte{0x96, 0x00, 0x05, 0x00, 0x41}, // too short for the 24-byte nick
	})
	assert.Error(t, err)
}

// TestWhisperHandler_DeadRecipient_Teardown asserts a recipient whose Conn
// write fails is unregistered (stale entry stops receiving) and the sender is
// still acked success.
func TestWhisperHandler_DeadRecipient_Teardown(t *testing.T) {
	t.Parallel()
	registry := domain.NewPlayerRegistry()
	senderConn := &captureConn{role: gwdomain.RoleMap, auth: gwdomain.ConnAuth{AccountID: 2000905}}
	require.NoError(t, registry.Register(&domain.Player{
		Conn: senderConn, EntityID: aoi.EntityID(2000905), AccountID: 2000905, CharID: 150009, MapName: "prontera", Name: "Sender5",
	}))
	deadConn := &failingConn{captureConn: captureConn{role: gwdomain.RoleMap}}
	require.NoError(t, registry.Register(&domain.Player{
		Conn: deadConn, EntityID: aoi.EntityID(2000906), AccountID: 2000906, CharID: 150010, MapName: "prontera", Name: "Dead",
	}))

	h := app.NewWhisperHandler(registry)
	err := h.Handle(context.Background(), senderConn, gwdomain.Frame{
		Cmd: packet.HeaderCZWHISPER,
		Raw: encodeWhisperReq(t, "Dead", "are you there"),
	})
	require.NoError(t, err)

	// Recipient torn down.
	_, ok := registry.ByAccount(2000906)
	assert.False(t, ok, "dead recipient unregistered after the failed write")
	// Sender still acked success (message was dispatched).
	ack := senderConn.buf.Bytes()
	require.Len(t, ack, 7, "sender acked despite the dead recipient")
	assert.Equal(t, uint8(0), ack[2], "ZC_ACK_WHISPER result = 0 (success, message dispatched)")
}
