//go:build unit

package app_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

const statsAccID uint32 = 2000900

// memCharStore is an in-memory character repository for stat-change tests.
// It holds one character keyed by (accountID, charID) and records the last
// Progression written so a test can assert persisted state.
type memCharStore struct {
	accountID uint32
	charID    uint32
	char      chardomain.Character
}

func (s *memCharStore) GetByID(_ context.Context, accountID, charID uint32) (*chardomain.Character, error) {
	if accountID != s.accountID || charID != s.charID {
		return nil, chardomain.ErrCharacterNotFound
	}
	cp := s.char
	return &cp, nil
}

func (s *memCharStore) SaveProgression(_ context.Context, accountID, charID uint32, p chardomain.Progression) error {
	if accountID != s.accountID || charID != s.charID {
		return chardomain.ErrCharacterNotFound
	}
	s.char.StatusPoint = p.StatusPoint
	s.char.Str = p.Str
	s.char.Agi = p.Agi
	s.char.Vit = p.Vit
	s.char.Int = p.Int
	s.char.Dex = p.Dex
	s.char.Luk = p.Luk
	return nil
}

func (s *memCharStore) ListByAccount(_ context.Context, accountID uint32) ([]chardomain.Character, error) {
	if accountID != s.accountID {
		return nil, nil
	}
	return []chardomain.Character{s.char}, nil
}

func (s *memCharStore) GetBySlot(_ context.Context, accountID uint32, slot uint8) (*chardomain.Character, error) {
	if accountID != s.accountID || slot != 0 {
		return nil, chardomain.ErrCharacterNotFound
	}
	return &s.char, nil
}

func (s *memCharStore) Create(_ context.Context, _ chardomain.CreateCharacter) (*chardomain.Character, error) {
	return &s.char, nil
}

func (s *memCharStore) SavePosition(_ context.Context, _, _ uint32, _ string, _, _ uint16) error {
	return nil
}

func (s *memCharStore) SaveLook(_ context.Context, _, _ uint32, _, _ uint16) error {
	return nil
}

type statsFixture struct {
	svc      *app.StatsService
	handler  *app.StatsHandler
	registry *domain.PlayerRegistry
	player   *domain.Player
	conn     *captureConn
	store    *memCharStore
}

func newStatsFixture(t *testing.T, char *chardomain.Character) statsFixture {
	t.Helper()
	store := &memCharStore{
		accountID: char.AccountID,
		charID:    char.CharID,
		char:      *char,
	}
	registry := domain.NewPlayerRegistry()
	conn := &captureConn{role: gwdomain.RoleMap, remote: "stats", auth: gwdomain.ConnAuth{AccountID: char.AccountID}}
	player := &domain.Player{
		Conn:      conn,
		EntityID:  aoi.EntityID(char.AccountID),
		AccountID: char.AccountID,
		CharID:    char.CharID,
		MapName:   "prontera",
		PosX:      100,
		PosY:      100,
	}
	require.NoError(t, registry.Register(player))
	svc := app.NewStatsService(store, registry)
	handler := app.NewStatsHandler(store, registry)
	return statsFixture{svc: svc, handler: handler, registry: registry, player: player, conn: conn, store: store}
}

// statChangeFrame encodes a CZ_STATUS_CHANGE frame for the given statID and amount.
func statChangeFrame(t *testing.T, statID uint16, amount uint8) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, packet.CZStatusChangeRequest{StatusID: statID, Amount: amount}.Encode(&buf))
	return buf.Bytes()
}

// expectStatusAck encodes a ZC_STATUS_CHANGE_ACK frame independently.
func expectStatusAck(t *testing.T, statusID uint16, result, value uint8) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (packet.ZCStatusChangeAck{StatusID: statusID, Result: result, Value: value}).Encode(&buf))
	return buf.Bytes()
}

// expectStatParChange encodes a ZC_PAR_CHANGE frame independently.
func expectStatParChange(t *testing.T, varID uint16, count int32) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (packet.ParChangeResponse{VarID: varID, Count: count}).Encode(&buf))
	return buf.Bytes()
}

func TestStatsService_IncreaseStat_ValidAllocation(t *testing.T) {
	t.Parallel()
	char := &chardomain.Character{
		CharID: 150010, AccountID: statsAccID,
		Str: 1, Agi: 1, Vit: 1, Int: 1, Dex: 1, Luk: 1,
		StatusPoint: 48,
	}
	f := newStatsFixture(t, char)

	// STR starts at 1; cost = [(1-1)/10]+2 = 2
	newVal, remaining, err := f.svc.IncreaseStat(context.Background(), statsAccID, 150010, 13) // SP_STR
	require.NoError(t, err)
	assert.Equal(t, uint16(2), newVal, "STR should be 2")
	assert.Equal(t, uint32(46), remaining, "StatusPoint deducted by 2 (48 - 2 = 46)")
}

func TestStatsService_IncreaseStat_InsufficientPoints(t *testing.T) {
	t.Parallel()
	char := &chardomain.Character{
		CharID: 150010, AccountID: statsAccID,
		Str: 1, Agi: 1, Vit: 1, Int: 1, Dex: 1, Luk: 1,
		StatusPoint: 1, // only 1 point, but cost is 2
	}
	f := newStatsFixture(t, char)

	_, _, err := f.svc.IncreaseStat(context.Background(), statsAccID, 150010, 13)
	assert.Error(t, err, "should fail with insufficient points")
	assert.Contains(t, err.Error(), "insufficient")
}

func TestStatsService_IncreaseStat_InvalidStatID(t *testing.T) {
	t.Parallel()
	char := &chardomain.Character{
		CharID: 150010, AccountID: statsAccID,
		StatusPoint: 100,
	}
	f := newStatsFixture(t, char)

	_, _, err := f.svc.IncreaseStat(context.Background(), statsAccID, 150010, 99) // invalid
	assert.Error(t, err, "should fail with invalid stat ID")
	assert.Contains(t, err.Error(), "invalid")
}

func TestStatsHandler_Handle_ValidSTR(t *testing.T) {
	t.Parallel()
	char := &chardomain.Character{
		CharID: 150010, AccountID: statsAccID,
		Str: 5, Agi: 1, Vit: 1, Int: 1, Dex: 1, Luk: 1,
		StatusPoint: 48,
	}
	f := newStatsFixture(t, char)

	err := f.handler.Handle(context.Background(), f.conn, gwdomain.Frame{Cmd: packet.HeaderCZSTATUSCHANGE, Raw: statChangeFrame(t, 13, 1)})
	require.NoError(t, err)

	frames := splitStatFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 3, "ack + SP_STR + SP_STATUSPOINT")

	assert.Equal(t, expectStatusAck(t, 13, 0, 6), frames[0], "ZC_STATUS_CHANGE_ACK success, STR=6")
	assert.Equal(t, expectStatParChange(t, packet.SPStr, 6), frames[1], "ZC_PAR_CHANGE SP_STR=6")
	assert.Equal(t, expectStatParChange(t, packet.SPStatusPoint, 46), frames[2], "ZC_PAR_CHANGE SP_STATUSPOINT=46")
}

func TestStatsHandler_Handle_ValidVIT(t *testing.T) {
	t.Parallel()
	char := &chardomain.Character{
		CharID: 150010, AccountID: statsAccID,
		Str: 1, Agi: 1, Vit: 3, Int: 1, Dex: 1, Luk: 1,
		StatusPoint: 48,
	}
	f := newStatsFixture(t, char)

	err := f.handler.Handle(context.Background(), f.conn, gwdomain.Frame{Cmd: packet.HeaderCZSTATUSCHANGE, Raw: statChangeFrame(t, 15, 1)}) // SP_VIT
	require.NoError(t, err)

	frames := splitStatFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 3)

	assert.Equal(t, expectStatusAck(t, 15, 0, 4), frames[0], "ZC_STATUS_CHANGE_ACK success, VIT=4")
	assert.Equal(t, expectStatParChange(t, packet.SPVit, 4), frames[1], "ZC_PAR_CHANGE SP_VIT=4")
}

func TestStatsHandler_Handle_InsufficientPoints(t *testing.T) {
	t.Parallel()
	char := &chardomain.Character{
		CharID: 150010, AccountID: statsAccID,
		Str: 1, Agi: 1, Vit: 1, Int: 1, Dex: 1, Luk: 1,
		StatusPoint: 0, // no points
	}
	f := newStatsFixture(t, char)

	err := f.handler.Handle(context.Background(), f.conn, gwdomain.Frame{Cmd: packet.HeaderCZSTATUSCHANGE, Raw: statChangeFrame(t, 13, 1)})
	require.NoError(t, err)

	frames := splitStatFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 1, "only failure ack; no par changes")

	assert.Equal(t, expectStatusAck(t, 13, 1, 0), frames[0], "ZC_STATUS_CHANGE_ACK result=1 (insufficient points)")
}

func TestStatsHandler_Handle_InvalidStatID(t *testing.T) {
	t.Parallel()
	char := &chardomain.Character{
		CharID: 150010, AccountID: statsAccID,
		Str: 1, Agi: 1, Vit: 1, Int: 1, Dex: 1, Luk: 1,
		StatusPoint: 100,
	}
	f := newStatsFixture(t, char)

	err := f.handler.Handle(context.Background(), f.conn, gwdomain.Frame{Cmd: packet.HeaderCZSTATUSCHANGE, Raw: statChangeFrame(t, 99, 1)})
	require.NoError(t, err)

	// Invalid stat ID is silently dropped (just return nil error)
	// No frames written because the stat ID was never found
	assert.Equal(t, 0, f.conn.buf.Len(), "no frames for invalid stat ID")
}

// splitStatFrames slices a captured byte stream of fixed-length map packets into
// per-frame slices. Stats emits only fixed-size packets:
// ZC_STATUS_CHANGE_ACK (6 bytes) and ZC_PAR_CHANGE (9 bytes).
func splitStatFrames(t *testing.T, b []byte) [][]byte {
	t.Helper()
	sizes := map[uint16]int{
		packet.HeaderZCSTATUSCHANGEACK: 6, // ZC_STATUS_CHANGE_ACK: [2:cmd][2:statusID][1:result][1:value]
		packet.HeaderZCPARCHANGE:       packet.ParChangeResponse{}.Size(),
	}
	var out [][]byte
	for len(b) >= 2 {
		cmd := binary.LittleEndian.Uint16(b[0:2])
		size, ok := sizes[cmd]
		if !ok {
			t.Fatalf("unknown cmd 0x%04x in captured stream", cmd)
		}
		if size > len(b) {
			t.Fatalf("frame for cmd 0x%04x overruns the buffer", cmd)
		}
		out = append(out, b[:size])
		b = b[size:]
	}
	return out
}
