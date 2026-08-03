//go:build unit

package app_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	"github.com/bouroo/goAthena/internal/modules/inventory/infra"
	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/equip"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	"github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/statcalc"
)

const (
	eqAccID  uint32 = 2000800
	eqCharID uint32 = 150003
)

// eqNoviceFistASPD mirrors the unexported noviceFistASPD constant in
// internal/modules/world/app/spawn.go (db/pre-re/job_aspd.yml BaseASPD.Fist =
// 500): the WeaponBaseASPD the EquipService threads into the ZC_STATUS recompute.
// It is restated here because it is unexported in package app; the value is what
// the test observes on the wire.
const eqNoviceFistASPD uint16 = 500

// equipItemDB is a minimal ITEM_DB v3 covering the equip paths: a Knife (weapon,
// ATK 17, level-1 usable, a look sprite), a Cotton_Shirt (armor, DEF 10), a
// high-min-level Dagger (the low-level fail), and a Jellopy (Etc — not wearable).
// Locations is a flow map the itemdb loader folds into EquipLocations via
// equip.LocationBits, so Right_Hand → EQP_HAND_R (0x2) and Armor → EQP_ARMOR
// (0x10) — the bitmasks the EquipService validates against.
func equipItemDB(t *testing.T) *itemdb.Registry {
	t.Helper()
	const yaml = `Header:
  Type: ITEM_DB
  Version: 3
Body:
  - {Id: 1201, AegisName: Knife,       Name: Knife,       Type: Weapon, Attack: 17, EquipLevelMin: 1, View: 1, Locations: {Right_Hand: true}}
  - {Id: 2389, AegisName: Cotton_Shirt, Name: Cotton_Shirt, Type: Armor,  Defense: 10, EquipLevelMin: 1, Locations: {Armor: true}}
  - {Id: 1202, AegisName: Dagger,      Name: Dagger,      Type: Weapon, Attack: 10, EquipLevelMin: 5, View: 2, Locations: {Right_Hand: true}}
  - {Id: 909,  AegisName: Jellopy,     Name: Jellopy,     Type: Etc}
`
	reg, err := itemdb.Load(strings.NewReader(yaml))
	require.NoError(t, err)
	return reg
}

// nakedNoviceChar is a level-1 Novice with all-1 base stats and 48 status points —
// what the world spawns. The equip tests recompute ZC_STATUS against it, so the
// expected frames share this base.
func nakedNoviceChar() *chardomain.Character {
	return &chardomain.Character{
		CharID: eqCharID, AccountID: eqAccID, BaseLevel: 1, StatusPoint: 48,
		Str: 1, Agi: 1, Vit: 1, Int: 1, Dex: 1, Luk: 1,
	}
}

type equipFixture struct {
	svc     *app.EquipService
	conn    *captureConn
	repo    *infra.MemoryInventoryRepository
	players *domain.PlayerRegistry
}

// newEquipFixture wires an EquipService over a memory bag, a capturing conn, and a
// fakeCharGetter returning the seeded char (the same CharacterGetter stand-in the
// spawn tests use). The player's CLevel mirrors the char's so the level gate reads
// the same value the recompute will.
func newEquipFixture(t *testing.T, char *chardomain.Character) equipFixture {
	t.Helper()
	repo := infra.NewMemoryInventoryRepository()
	players := domain.NewPlayerRegistry()
	conn := &captureConn{role: gwdomain.RoleMap, remote: "equip"}
	player := &domain.Player{
		Conn:      conn,
		EntityID:  aoi.EntityID(eqAccID),
		AccountID: eqAccID, CharID: eqCharID, MapName: "prontera",
		CLevel: char.BaseLevel,
		Str:    char.Str, Agi: char.Agi, Vit: char.Vit, Int: char.Int, Dex: char.Dex, Luk: char.Luk,
	}
	require.NoError(t, players.Register(player))
	chars := &fakeCharGetter{chars: map[uint64]chardomain.Character{charKey(eqAccID, eqCharID): *char}}
	svc := app.NewEquipService(repo, equipItemDB(t), players, chars, statcalc.PreRenewalSet, nil)
	return equipFixture{svc: svc, conn: conn, repo: repo, players: players}
}

// addBagItem inserts one non-stackable row and returns its grid index.
func addBagItem(t *testing.T, f equipFixture, nameID uint32) uint16 {
	t.Helper()
	_, err := f.repo.AddItem(context.Background(), eqAccID, eqCharID, invdomain.NewItem{NameID: nameID, Amount: 1, Stackable: false})
	require.NoError(t, err)
	rows, err := f.repo.LoadByChar(context.Background(), eqAccID, eqCharID)
	require.NoError(t, err)
	for _, r := range rows {
		if r.NameID == nameID {
			return r.Index
		}
	}
	t.Fatalf("addBagItem: nameid %d not found after add", nameID)
	return 0
}

// expectWearEquipAck encodes the ZC_REQ_WEAR_EQUIP_ACK_V5 frame (0x0999, 11B) the
// EquipService emits.
func expectWearEquipAck(t *testing.T, index uint16, wear uint32, sprite uint16, result uint8) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (&packet.ReqWearEquipAckResponse{
		Index: index, WearLocation: wear, ItemSpriteNumber: sprite, Result: result,
	}).Encode(&buf))
	return buf.Bytes()
}

// expectTakeoffAck encodes the ZC_REQ_TAKEOFF_EQUIP_ACK frame (0x099a, 9B) with
// the wire flag the EquipService writes (0 = success, 1 = fail).
func expectTakeoffAck(t *testing.T, index uint16, wear uint32, flag uint8) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (&packet.ReqTakeoffEquipAckResponse{
		Index: index, WearLocation: wear, Flag: flag,
	}).Encode(&buf))
	return buf.Bytes()
}

// expectEquipStatus encodes the ZC_STATUS frame the recompute emits, folding the
// given weapon ATK / armor DEF into the Equipment the EquipService builds. It
// shares the char base and the novice-fist ASPD with the service, so a match is
// byte-exact.
func expectEquipStatus(t *testing.T, char *chardomain.Character, weaponATK, itemDEF int32) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (statcalc.ZCStatus(statcalc.StatusInputs{
		Base: statcalc.Base{
			Level: char.BaseLevel,
			Str:   char.Str, Agi: char.Agi, Vit: char.Vit,
			Int: char.Int, Dex: char.Dex, Luk: char.Luk,
		},
		StatusPoint:    char.StatusPoint,
		Equipment:      statcalc.Equipment{WeaponATK: weaponATK, ItemDEF: itemDEF},
		WeaponBaseASPD: eqNoviceFistASPD,
	}, statcalc.PreRenewalSet)).Encode(&buf))
	return buf.Bytes()
}

// splitEquipFrames walks the captured buffer, reading each frame's header to pick
// its fixed size. Only the three fixed-size packet types the equip path emits are
// recognized — unlike spawn's length-prefixed splitFrames, these carry field data
// (not a length) at [2:4], so the sizes are looked up by header.
func splitEquipFrames(buf []byte) [][]byte {
	var frames [][]byte
	for i := 0; i+2 <= len(buf); {
		hdr := binary.LittleEndian.Uint16(buf[i:])
		var size int
		switch hdr {
		case packet.HeaderZCREQWEAREQUIPACKV5:
			size = packet.ReqWearEquipAckResponse{}.Size()
		case packet.HeaderZCREQTAKEOFFEQUIPACK:
			size = packet.ReqTakeoffEquipAckResponse{}.Size()
		case packet.HeaderZCSTATUS:
			size = packet.StatusResponse{}.Size()
		default:
			return frames
		}
		if i+size > len(buf) {
			return frames
		}
		frames = append(frames, buf[i:i+size])
		i += size
	}
	return frames
}

// statusATK decodes Atk1/Atk2 from a ZC_STATUS frame (offsets 16/18 per
// packets.hpp:909-938).
func statusATK(frame []byte) (atk1, atk2 int16) {
	return int16(binary.LittleEndian.Uint16(frame[16:18])), //nolint:gosec // G115: wire field is int16; ATK fits
		int16(binary.LittleEndian.Uint16(frame[18:20])) //nolint:gosec // G115: wire field is int16; ATK fits
}

// TestEquipService_ValidWeapon_AcksAndRaisesWeaponATK is the M10b anchor: a
// level-1 char equips a Knife (ATK 17) and the client gets the success ack
// (result=1, the Knife's look sprite, the right-hand slot) plus a ZC_STATUS whose
// right-side ATK rose from 0 to 17 while the left-side ATK stays the naked value.
func TestEquipService_ValidWeapon_AcksAndRaisesWeaponATK(t *testing.T) {
	t.Parallel()
	char := nakedNoviceChar()
	f := newEquipFixture(t, char)
	idx := addBagItem(t, f, 1201) // Knife

	require.NoError(t, f.svc.Equip(context.Background(), eqAccID, idx, equip.HandRight))

	frames := splitEquipFrames(f.conn.buf.Bytes())
	require.Len(t, frames, 2, "wear ack + ZC_STATUS")
	assert.Equal(t, expectWearEquipAck(t, idx, equip.HandRight, 1 /*Knife View*/, 1), frames[0])

	// Right-side ATK is the Knife's 17; left-side ATK is the naked BaseATK
	// (equipping raises Atk2, never Atk1).
	assert.Equal(t, expectEquipStatus(t, char, 17, 0), frames[1])
	nakedAtk1, nakedAtk2 := statusATK(expectEquipStatus(t, char, 0, 0))
	equippedAtk1, equippedAtk2 := statusATK(frames[1])
	assert.Equal(t, int16(17), equippedAtk2, "weapon ATK folded into Atk2")
	assert.Equal(t, int16(0), nakedAtk2, "naked Atk2 is 0 (no weapon)")
	assert.Equal(t, nakedAtk1, equippedAtk1, "left-side ATK unchanged by equipping")

	// The worn bitmask persisted so a reload reflects it.
	rows, err := f.repo.LoadByChar(context.Background(), eqAccID, eqCharID)
	require.NoError(t, err)
	require.True(t, rows[idx].Equip == equip.HandRight, "Knife row carries the right-hand bitmask")
}

// TestEquipService_WeaponAndArmor_FoldsAtkAndDef asserts equipmentStats folds a
// weapon's ATK into Atk2 and an armor's DEF into Def1 — the two Equipment fields
// ZC_STATUS carries separately from the naked base.
func TestEquipService_WeaponAndArmor_FoldsAtkAndDef(t *testing.T) {
	t.Parallel()
	char := nakedNoviceChar()
	f := newEquipFixture(t, char)
	knife := addBagItem(t, f, 1201)
	shirt := addBagItem(t, f, 2389) // Cotton_Shirt

	require.NoError(t, f.svc.Equip(context.Background(), eqAccID, knife, equip.HandRight))
	f.conn.buf.Reset()
	require.NoError(t, f.svc.Equip(context.Background(), eqAccID, shirt, equip.Armor))

	frames := splitEquipFrames(f.conn.buf.Bytes())
	require.Len(t, frames, 2)
	// The second equip's recompute sees BOTH items worn: weapon ATK + armor DEF.
	assert.Equal(t, expectEquipStatus(t, char, 17 /*Knife*/, 10 /*Cotton_Shirt*/), frames[1])
	_, atk2 := statusATK(frames[1])
	assert.Equal(t, int16(17), atk2, "weapon ATK present after also equipping armor")
}

// TestEquipService_LowLevelWeapon_FailAck asserts the level gate (pc.cpp:14800):
// a weapon whose EquipLevelMin exceeds the char's base level answers result=2
// (low-level), never equips, and emits no ZC_STATUS.
func TestEquipService_LowLevelWeapon_FailAck(t *testing.T) {
	t.Parallel()
	char := nakedNoviceChar() // BaseLevel 1
	f := newEquipFixture(t, char)
	idx := addBagItem(t, f, 1202) // Dagger, EquipLevelMin 5

	require.NoError(t, f.svc.Equip(context.Background(), eqAccID, idx, equip.HandRight))

	frames := splitEquipFrames(f.conn.buf.Bytes())
	require.Len(t, frames, 1, "only the low-level fail ack")
	assert.Equal(t, expectWearEquipAck(t, idx, 0, 0, 2), frames[0])

	rows, err := f.repo.LoadByChar(context.Background(), eqAccID, eqCharID)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), rows[idx].Equip, "low-level item never equipped")
}

// TestEquipService_NotEquipment_FailAck asserts an Etc item (EquipLocations 0) is
// rejected with result=0 — the "not wearable" branch.
func TestEquipService_NotEquipment_FailAck(t *testing.T) {
	t.Parallel()
	f := newEquipFixture(t, nakedNoviceChar())
	idx := addBagItem(t, f, 909) // Jellopy, Etc

	require.NoError(t, f.svc.Equip(context.Background(), eqAccID, idx, equip.HandRight))

	frames := splitEquipFrames(f.conn.buf.Bytes())
	require.Len(t, frames, 1)
	assert.Equal(t, expectWearEquipAck(t, idx, 0, 0, 0), frames[0])
}

// TestEquipService_UnknownItem_FailAck asserts a bag row whose NameID is absent
// from the item_db answers result=0 rather than faulting.
func TestEquipService_UnknownItem_FailAck(t *testing.T) {
	t.Parallel()
	f := newEquipFixture(t, nakedNoviceChar())
	idx := addBagItem(t, f, 9999) // not in the test item_db

	require.NoError(t, f.svc.Equip(context.Background(), eqAccID, idx, equip.HandRight))

	frames := splitEquipFrames(f.conn.buf.Bytes())
	require.Len(t, frames, 1)
	assert.Equal(t, expectWearEquipAck(t, idx, 0, 0, 0), frames[0])
}

// TestEquipService_SlotNotPermitted_FailAck asserts pc_equippoint_check: a
// permitted-slot intersection of 0 (requesting the armor slot for a hand weapon)
// answers result=0.
func TestEquipService_SlotNotPermitted_FailAck(t *testing.T) {
	t.Parallel()
	f := newEquipFixture(t, nakedNoviceChar())
	idx := addBagItem(t, f, 1201) // Knife: Right_Hand only

	require.NoError(t, f.svc.Equip(context.Background(), eqAccID, idx, equip.Armor))

	frames := splitEquipFrames(f.conn.buf.Bytes())
	require.Len(t, frames, 1)
	assert.Equal(t, expectWearEquipAck(t, idx, 0, 0, 0), frames[0])
}

// TestEquipService_StaleIndex_FailAck asserts a forged/stale grid index (past the
// last row) answers result=0.
func TestEquipService_StaleIndex_FailAck(t *testing.T) {
	t.Parallel()
	f := newEquipFixture(t, nakedNoviceChar())
	addBagItem(t, f, 1201) // one row at index 0

	require.NoError(t, f.svc.Equip(context.Background(), eqAccID, 5, equip.HandRight))

	frames := splitEquipFrames(f.conn.buf.Bytes())
	require.Len(t, frames, 1)
	assert.Equal(t, expectWearEquipAck(t, 5, 0, 0, 0), frames[0])
}

// TestEquipService_Unequip_DropsWeaponATKToZero asserts the takeoff loop: clearing
// the worn bit answers the success ack (wire flag 0) and a ZC_STATUS whose
// right-side ATK returns to 0.
func TestEquipService_Unequip_DropsWeaponATKToZero(t *testing.T) {
	t.Parallel()
	char := nakedNoviceChar()
	f := newEquipFixture(t, char)
	idx := addBagItem(t, f, 1201)
	require.NoError(t, f.svc.Equip(context.Background(), eqAccID, idx, equip.HandRight))

	f.conn.buf.Reset()
	require.NoError(t, f.svc.Unequip(context.Background(), eqAccID, idx))

	frames := splitEquipFrames(f.conn.buf.Bytes())
	require.Len(t, frames, 2, "takeoff ack + ZC_STATUS")
	assert.Equal(t, expectTakeoffAck(t, idx, equip.HandRight, 0 /*success*/), frames[0])
	assert.Equal(t, expectEquipStatus(t, char, 0, 0), frames[1])
	_, atk2 := statusATK(frames[1])
	assert.Equal(t, int16(0), atk2, "weapon ATK cleared after unequip")

	rows, err := f.repo.LoadByChar(context.Background(), eqAccID, eqCharID)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), rows[idx].Equip, "Knife slot cleared after unequip")
}

// TestEquipService_Unequip_NotWorn_FailAck asserts taking off a slot that is not
// worn answers the wire-inverted fail (flag=1).
func TestEquipService_Unequip_NotWorn_FailAck(t *testing.T) {
	t.Parallel()
	f := newEquipFixture(t, nakedNoviceChar())
	idx := addBagItem(t, f, 1201) // present but not worn

	require.NoError(t, f.svc.Unequip(context.Background(), eqAccID, idx))

	frames := splitEquipFrames(f.conn.buf.Bytes())
	require.Len(t, frames, 1, "only the takeoff fail ack")
	assert.Equal(t, expectTakeoffAck(t, idx, 0, 1 /*fail*/), frames[0])
}

// TestEquipService_NoSession_Noop asserts an account with no live player is a
// silent drop (nothing written, no error).
func TestEquipService_NoSession_Noop(t *testing.T) {
	t.Parallel()
	f := newEquipFixture(t, nakedNoviceChar())

	require.NoError(t, f.svc.Equip(context.Background(), 999999, 0, equip.HandRight))
	assert.Empty(t, f.conn.buf.Bytes(), "no frames for an unknown account")
}

// TestEquipHandler_NoVerifiedAccount asserts the handler rejects a request on a
// connection that never passed CZ_ENTER (no cached account) with an error so
// ProcessBytes logs it.
func TestEquipHandler_NoVerifiedAccount(t *testing.T) {
	t.Parallel()
	f := newEquipFixture(t, nakedNoviceChar())
	h := app.NewEquipHandler(f.svc)

	var req bytes.Buffer
	require.NoError(t, (&packet.CZReqWearEquipRequest{Index: 0, Position: equip.HandRight}).Encode(&req))

	err := h.Handle(context.Background(),
		&captureConn{role: gwdomain.RoleMap, remote: "x"},
		gwdomain.Frame{Cmd: packet.HeaderCZREQWEAREQUIPV5, Raw: req.Bytes()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no verified account")
}
