//go:build unit

package app

import (
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/bouroo/goAthena/internal/modules/content/domain"
	transitdomain "github.com/bouroo/goAthena/internal/modules/transit/domain"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

// warpCall records one ScriptHost.Warp -> ScriptWorld.WarpPlayer call.
type warpCall struct {
	charID  uint32
	mapName string
	x, y    int16
}

// healCall records one ScriptHost.PercentHeal -> ScriptWorld.HealPlayer call.
type healCall struct {
	charID       uint32
	hpPct, spPct int
}

// leaveCall records one ScriptHost redirect -> ScriptWorld.LeaveRemoteZone call.
type leaveCall struct {
	charID uint32
	x, y   int16
}

// announceCall records one ScriptHost.Announce -> ScriptWorld.Announce call.
type announceCall struct {
	anchorID uint32
	msg      string
	flag     int
}

// mapAnnounceCall records one ScriptHost.AnnounceMap -> ScriptWorld.AnnounceMap.
type mapAnnounceCall struct {
	mapName string
	msg     string
	flag    int
}

// fakeScriptWorld is an in-memory ScriptWorld: it records effect calls and
// returns scripted HP/SP (and err) for heals. err, when set, makes both methods
// fail so the ScriptHost exercises its drop-frame branch. zone, when non-nil,
// makes ResolveZone return it (the cross-zone redirect branch); leaveErr fails
// the redirect's LeaveRemoteZone.
type fakeScriptWorld struct {
	warps        []warpCall
	heals        []healCall
	leaves       []leaveCall
	announces    []announceCall
	mapAnnounces []mapAnnounceCall
	absHeals     []healCall
	hp           int32
	sp           int32
	err          error
	zone         *transitdomain.Zone
	leaveErr     error
}

func (f *fakeScriptWorld) WarpPlayer(charID uint32, mapName string, x, y int16) error {
	f.warps = append(f.warps, warpCall{charID: charID, mapName: mapName, x: x, y: y})
	return f.err
}

func (f *fakeScriptWorld) ResolveZone(mapName string) (transitdomain.Zone, error) {
	if f.zone == nil {
		return transitdomain.Zone{}, nil
	}
	return *f.zone, nil
}

func (f *fakeScriptWorld) LeaveRemoteZone(_ context.Context, charID uint32, x, y int16) error {
	f.leaves = append(f.leaves, leaveCall{charID: charID, x: x, y: y})
	return f.leaveErr
}

func (f *fakeScriptWorld) HealPlayer(charID uint32, hpPct, spPct int) (int32, int32, error) {
	f.heals = append(f.heals, healCall{charID: charID, hpPct: hpPct, spPct: spPct})
	return f.hp, f.sp, f.err
}

// Announce records one script broadcast. Audience resolution (BC_SELF/MAP/AREA/
// ALL) lives in the world module and is covered by its own tests; this fake only
// shows what the ScriptHost handed over.
func (f *fakeScriptWorld) Announce(anchorID uint32, msg string, flag int) {
	f.announces = append(f.announces, announceCall{anchorID: anchorID, msg: msg, flag: flag})
}

func (f *fakeScriptWorld) AnnounceMap(mapName, msg string, flag int) {
	f.mapAnnounces = append(f.mapAnnounces, mapAnnounceCall{mapName: mapName, msg: msg, flag: flag})
}

// HealAbs records one absolute restore and returns f.err, mirroring HealPlayer.
func (f *fakeScriptWorld) HealAbs(charID uint32, hp, sp int) error {
	f.absHeals = append(f.absHeals, healCall{charID: charID, hpPct: hp, spPct: sp})
	return f.err
}

// captureWriter buffers each WritePacket payload as a separate frame.
type captureWriter struct {
	frames [][]byte
}

func (w *captureWriter) WritePacket(data []byte) {
	cp := make([]byte, len(data))
	copy(cp, data)
	w.frames = append(w.frames, cp)
}

// newTestHost builds a ScriptHost over world with a nil item_db registry; the
// getiteminfo tests use newTestHostWithItems.
func newTestHost(world domain.ScriptWorld, charID uint32) (*ScriptHost, *captureWriter) {
	return newTestHostWithItems(world, charID, nil)
}

func newTestHostWithItems(world domain.ScriptWorld, charID uint32, items *itemdb.Registry) (*ScriptHost, *captureWriter) {
	w := &captureWriter{}
	sess := &domain.DialogSession{CharID: charID, Writer: w, Signal: make(chan domain.DialogSignal, 1)}
	return &ScriptHost{session: sess, world: world, items: items, log: slog.Default()}, w
}

func TestScriptHost_Warp(t *testing.T) {
	fw := &fakeScriptWorld{}
	h, w := newTestHost(fw, 150001)

	h.Warp("prontera", 100, 200)

	if len(fw.warps) != 1 {
		t.Fatalf("warps = %d, want 1", len(fw.warps))
	}
	if got := fw.warps[0]; got.charID != 150001 || got.mapName != "prontera" || got.x != 100 || got.y != 200 {
		t.Errorf("warp call = %+v, want {150001 prontera 100 200}", got)
	}
	if len(w.frames) != 1 {
		t.Fatalf("frames = %d, want 1 (ZC_NPCACK_MAPMOVE)", len(w.frames))
	}
	fr := w.frames[0]
	if got := binary.LittleEndian.Uint16(fr[0:]); got != ropacket.HeaderZCNPCACKMAPMOVE {
		t.Errorf("header = %#x, want %#x", got, ropacket.HeaderZCNPCACKMAPMOVE)
	}
	if got := strings.TrimRight(string(fr[2:18]), "\x00"); got != "prontera" { // map name zero-padded in a 16-byte slot
		t.Errorf("map name = %q, want %q", got, "prontera")
	}
	if got := binary.LittleEndian.Uint16(fr[18:]); got != 100 {
		t.Errorf("x = %d, want 100", got)
	}
	if got := binary.LittleEndian.Uint16(fr[20:]); got != 200 {
		t.Errorf("y = %d, want 200", got)
	}
}

// remote is a non-zero zone marker: any address means "another zone process".
var remote = transitdomain.Zone{Name: "zone-b", IPv4: 0x7f000001, Port: 0x1401}

func TestScriptHost_WarpRemoteZoneRedirects(t *testing.T) {
	fw := &fakeScriptWorld{zone: &remote}
	h, w := newTestHost(fw, 150001)

	h.Warp("geffen", 50, 60)

	// The redirect persists the destination (WarpPlayer), then tears the
	// player out of THIS zone's world at the destination cell.
	if len(fw.warps) != 1 {
		t.Fatalf("warps = %d, want 1 (destination persist)", len(fw.warps))
	}
	if got := fw.warps[0]; got.charID != 150001 || got.mapName != "geffen" || got.x != 50 || got.y != 60 {
		t.Errorf("warp call = %+v, want {150001 geffen 50 60}", got)
	}
	if len(fw.leaves) != 1 {
		t.Fatalf("leaves = %d, want 1", len(fw.leaves))
	}
	if len(w.frames) != 1 {
		t.Fatalf("frames = %d, want 1 (ZC_NPCACK_SERVERMOVE)", len(w.frames))
	}
	fr := w.frames[0]
	if got := binary.LittleEndian.Uint16(fr[0:]); got != ropacket.HeaderZCNPCACKSERVERMOVE {
		t.Errorf("header = %#x, want %#x", got, ropacket.HeaderZCNPCACKSERVERMOVE)
	}
	if len(fr) != 156 {
		t.Errorf("frame len = %d, want 156", len(fr))
	}
	if got := strings.TrimRight(string(fr[2:26]), "\x00"); got != "geffen" {
		t.Errorf("map name = %q, want %q (24-byte slot)", got, "geffen")
	}
	if got := binary.LittleEndian.Uint16(fr[26:]); got != 50 {
		t.Errorf("x = %d, want 50", got)
	}
	if got := binary.LittleEndian.Uint16(fr[28:]); got != 60 {
		t.Errorf("y = %d, want 60", got)
	}
	if got := binary.BigEndian.Uint32(fr[30:]); got != 0x7f000001 {
		t.Errorf("ip = %#x, want 0x7f000001 (big-endian wire)", got)
	}
	if got := binary.BigEndian.Uint16(fr[34:]); got != 0x1401 {
		t.Errorf("port = %#x, want 0x1401 (byte-swapped wire)", got)
	}
}

func TestScriptHost_WarpRemoteLeaveFailsDropsFrame(t *testing.T) {
	fw := &fakeScriptWorld{zone: &remote, leaveErr: errors.New("db down")}
	h, w := newTestHost(fw, 150001)

	h.Warp("geffen", 50, 60)

	if len(w.frames) != 0 {
		t.Fatalf("frames = %d, want 0 (failed leave drops the redirect)", len(w.frames))
	}
	// The destination persist landed but the client was never redirected: the
	// persisted position is harmless (the player is still here, and the next
	// successful warp or the local leave overwrites it).
	if len(fw.warps) != 1 {
		t.Errorf("warps = %d, want 1 (persist precedes the failed leave)", len(fw.warps))
	}
}

func TestScriptHost_PercentHeal(t *testing.T) {
	fw := &fakeScriptWorld{hp: 750, sp: 45}
	h, w := newTestHost(fw, 150001)

	h.PercentHeal(100, 100)

	if len(fw.heals) != 1 {
		t.Fatalf("heals = %d, want 1", len(fw.heals))
	}
	if got := fw.heals[0]; got.charID != 150001 || got.hpPct != 100 || got.spPct != 100 {
		t.Errorf("heal call = %+v, want {150001 100 100}", got)
	}
	if len(w.frames) != 1 {
		t.Fatalf("frames = %d, want 1 (HP+SP ZC_PAR_CHANGE)", len(w.frames))
	}
	fr := w.frames[0] // two 8-byte ParChange packets concatenated
	if binary.LittleEndian.Uint16(fr[0:]) != ropacket.HeaderZCPARCHANGE {
		t.Errorf("hp packet header = %#x, want %#x", binary.LittleEndian.Uint16(fr[0:]), ropacket.HeaderZCPARCHANGE)
	}
	if got := binary.LittleEndian.Uint16(fr[2:]); got != ropacket.SPHP {
		t.Errorf("hp varID = %d, want SP_HP(%d)", got, ropacket.SPHP)
	}
	if got := int32(binary.LittleEndian.Uint32(fr[4:])); got != 750 { //nolint:gosec // test decode of int32 vital
		t.Errorf("hp count = %d, want 750", got)
	}
	if got := binary.LittleEndian.Uint16(fr[10:]); got != ropacket.SPSP {
		t.Errorf("sp varID = %d, want SP_SP(%d)", got, ropacket.SPSP)
	}
	if got := int32(binary.LittleEndian.Uint32(fr[12:])); got != 45 { //nolint:gosec // test decode of int32 vital
		t.Errorf("sp count = %d, want 45", got)
	}
}

func TestScriptHost_NilWorldNoOp(t *testing.T) {
	h, w := newTestHost(nil, 150001)
	h.Warp("prontera", 1, 2) // must not panic or emit a frame
	h.PercentHeal(100, 100)
	if len(w.frames) != 0 {
		t.Errorf("frames = %d, want 0 (no world wired)", len(w.frames))
	}
}

func TestScriptHost_DropsFrameWhenPlayerNotOnMap(t *testing.T) {
	fw := &fakeScriptWorld{err: errors.New("entity not found")}
	h, w := newTestHost(fw, 150001)
	h.Warp("prontera", 1, 2) // world returns err -> frame dropped
	h.PercentHeal(100, 100)  // world returns err -> frame dropped
	if len(w.frames) != 0 {
		t.Errorf("frames = %d, want 0 (world error drops frame)", len(w.frames))
	}
}

// TestScriptHost_CompiledWarpHealScript is the cheap e2e: a compiled NPC script
// drives warp+percentheal through the VM into the real ScriptHost, proving the
// VM builtin -> Host -> world-port + packet-emission path end to end.
func TestScriptHost_CompiledWarpHealScript(t *testing.T) {
	fw := &fakeScriptWorld{hp: 1000, sp: 100}
	h, w := newTestHost(fw, 150001)

	const src = "-\tscript\tHealer\t-1,{\n" +
		`percentheal 100,100;` + "\n" +
		`warp "izlude", 128, 200;` + "\n" +
		"}\n"
	set, err := script.Compile([]byte(src))
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	var cs *script.CompiledScript
	for _, s := range set.Scripts {
		cs = s
		break
	}
	vm := script.NewVM(cs, h, script.DefaultBuiltins(), nil)
	if err := vm.Run(); err != nil {
		t.Fatalf("Run error: %v", err)
	}

	if len(fw.heals) != 1 || fw.heals[0].hpPct != 100 || fw.heals[0].spPct != 100 {
		t.Errorf("heals = %+v, want one {100 100}", fw.heals)
	}
	if len(fw.warps) != 1 || fw.warps[0].mapName != "izlude" || fw.warps[0].x != 128 || fw.warps[0].y != 200 {
		t.Errorf("warps = %+v, want one {izlude 128 200}", fw.warps)
	}
	// heal frame (2 parchanges) then warp frame (mapmove)
	if len(w.frames) != 2 {
		t.Fatalf("frames = %d, want 2 (heal then warp)", len(w.frames))
	}
	if got := binary.LittleEndian.Uint16(w.frames[0][0:]); got != ropacket.HeaderZCPARCHANGE {
		t.Errorf("frame 0 header = %#x, want ZC_PAR_CHANGE", got)
	}
	if got := binary.LittleEndian.Uint16(w.frames[1][0:]); got != ropacket.HeaderZCNPCACKMAPMOVE {
		t.Errorf("frame 1 header = %#x, want ZC_NPCACK_MAPMOVE", got)
	}
}

// TestScriptHost_InputStr drives InputStr with a fake string reply and asserts it
// returns the exact text the client typed, not a numeric encoding of it.
func TestScriptHost_InputStr(t *testing.T) {
	h, w := newTestHost(nil, 150001)

	// Buffer the client's non-numeric string reply before the blocking call.
	h.session.Signal <- domain.DialogSignal{Input: "the quick brown fox"}

	got, ok := h.InputStr()
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if got != "the quick brown fox" {
		t.Errorf("InputStr = %q, want %q (raw string, not numeric encoding)", got, "the quick brown fox")
	}
	// InputStr must emit the string-input prompt (ZC_OPEN_EDITDLGSTR).
	if len(w.frames) != 1 {
		t.Fatalf("frames = %d, want 1 (ZC_OPEN_EDITDLGSTR)", len(w.frames))
	}
	if got := binary.LittleEndian.Uint16(w.frames[0][0:]); got != ropacket.HeaderZCOPENEDITDLGSTR {
		t.Errorf("frame 0 header = %#x, want ZC_OPEN_EDITDLGSTR", got)
	}
}

// TestScriptHost_InputStr_Cancel asserts a cancel/close signal yields "", false.
func TestScriptHost_InputStr_Cancel(t *testing.T) {
	h, _ := newTestHost(nil, 150001)

	h.session.Signal <- domain.DialogSignal{Cancel: true}

	got, ok := h.InputStr()
	if ok {
		t.Errorf("ok = true, want false on cancel")
	}
	if got != "" {
		t.Errorf("InputStr = %q, want \"\" on cancel", got)
	}
}

// TestScriptHost_Input guards the numeric sibling: a numeric reply still parses.
func TestScriptHost_Input(t *testing.T) {
	h, w := newTestHost(nil, 150001)

	h.session.Signal <- domain.DialogSignal{Input: "12345"}

	got, ok := h.Input()
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if got != 12345 {
		t.Errorf("Input = %d, want 12345", got)
	}
	if len(w.frames) != 1 {
		t.Fatalf("frames = %d, want 1 (ZC_OPEN_EDITDLG)", len(w.frames))
	}
	if got := binary.LittleEndian.Uint16(w.frames[0][0:]); got != ropacket.HeaderZCOPENEDITDLG {
		t.Errorf("frame 0 header = %#x, want ZC_OPEN_EDITDLG", got)
	}
}

// TestScriptHost_HealAbs proves the flat heal reaches the world port with its
// sign and amounts intact.
func TestScriptHost_HealAbs(t *testing.T) {
	fw := &fakeScriptWorld{}
	h, _ := newTestHost(fw, 150001)

	h.HealAbs(60, 20)

	if len(fw.absHeals) != 1 {
		t.Fatalf("abs heals = %d, want 1", len(fw.absHeals))
	}
	if got := fw.absHeals[0]; got.charID != 150001 || got.hpPct != 60 || got.spPct != 20 {
		t.Errorf("heal = %+v, want {150001 60 20}", got)
	}
}

// TestScriptHost_Announce proves the anchor follows the BC_NPC bit: an NPC
// source anchors on the dialog's NPC, everything else on the player.
func TestScriptHost_Announce(t *testing.T) {
	fw := &fakeScriptWorld{}
	h, _ := newTestHost(fw, 150001)
	h.session.NpcID = 900001

	h.Announce("player sourced", 0)      // BC_ALL, no BC_NPC
	h.Announce("npc sourced", 0x08|0x01) // BC_NPC|BC_MAP

	if len(fw.announces) != 2 {
		t.Fatalf("announces = %d, want 2", len(fw.announces))
	}
	if got := fw.announces[0]; got.anchorID != 150001 || got.flag != 0 {
		t.Errorf("announce[0] = %+v, want anchor 150001 flag 0", got)
	}
	if got := fw.announces[1]; got.anchorID != 900001 || got.flag != 0x09 {
		t.Errorf("announce[1] = %+v, want anchor 900001 flag 0x09", got)
	}
}

// TestScriptHost_AnnounceMap proves mapannounce passes the map through verbatim.
func TestScriptHost_AnnounceMap(t *testing.T) {
	fw := &fakeScriptWorld{}
	h, _ := newTestHost(fw, 150001)

	h.AnnounceMap("prontera", "hello map", 1)

	if len(fw.mapAnnounces) != 1 {
		t.Fatalf("map announces = %d, want 1", len(fw.mapAnnounces))
	}
	if got := fw.mapAnnounces[0]; got != (mapAnnounceCall{"prontera", "hello map", 1}) {
		t.Errorf("map announce = %+v", got)
	}
}

// itemFixtureYAML is a two-item item_db: a weapon with rich scalar columns and
// a healing potion, enough to exercise both getiteminfo lookup paths.
const itemFixtureYAML = `Header:
  Type: ITEM_DB
  Version: 3

Body:
  - Id: 1101
    AegisName: Sword
    Name: Sword
    Type: Weapon
    SubType: 1hSword
    Buy: 100
    Attack: 25
    Weight: 500
  - Id: 501
    AegisName: Red_Potion
    Name: Red Potion
    Type: Healing
    Buy: 50
`

// TestScriptHost_ItemInfo proves the three lookups rAthena supports: a numeric
// name id, an AegisName string, and an unknown item.
func TestScriptHost_ItemInfo(t *testing.T) {
	reg, err := itemdb.Load(strings.NewReader(itemFixtureYAML))
	if err != nil {
		t.Fatalf("itemdb load: %v", err)
	}
	h, _ := newTestHostWithItems(&fakeScriptWorld{}, 150001, reg)

	if got := h.ItemInfo(script.IntVal(1101), itemdb.InfoID); got.Kind != script.KindInt || got.Int != 1101 {
		t.Errorf("Info(numeric id) = %+v, want 1101", got)
	}
	if got := h.ItemInfo(script.IntVal(501), itemdb.InfoBuy); got.Int != 50 {
		t.Errorf("Info(501 buy) = %+v, want 50", got)
	}
	// A string argument resolves by AegisName (rAthena's searchname path).
	if got := h.ItemInfo(script.StrVal("Red_Potion"), itemdb.InfoAegisName); got.Kind != script.KindStr || got.Str != "Red_Potion" {
		t.Errorf("Info(aegis) = %+v, want \"Red_Potion\"", got)
	}
	// An AegisName lookup on a numeric row still resolves the same entry.
	if got := h.ItemInfo(script.StrVal("Sword"), itemdb.InfoSubType); got.Int != 2 {
		t.Errorf("Info(Sword subtype) = %+v, want 2 (W_1HSWORD)", got)
	}
	// Unknown item: -1 for a numeric column, "" for the string column.
	if got := h.ItemInfo(script.IntVal(999999), itemdb.InfoBuy); got.Kind != script.KindInt || got.Int != -1 {
		t.Errorf("Info(unknown) = %+v, want -1", got)
	}
	if got := h.ItemInfo(script.IntVal(999999), itemdb.InfoAegisName); got.Kind != script.KindStr || got.Str != "" {
		t.Errorf("Info(unknown aegis) = %+v, want \"\"", got)
	}
	// An unsupported column is -1 too.
	if got := h.ItemInfo(script.IntVal(1101), 999); got.Int != -1 {
		t.Errorf("Info(bad column) = %+v, want -1", got)
	}
}

// TestScriptHost_ItemInfo_NilRegistry proves a missing item_db degrades to the
// unknown-item answer instead of panicking.
func TestScriptHost_ItemInfo_NilRegistry(t *testing.T) {
	h, _ := newTestHostWithItems(&fakeScriptWorld{}, 150001, nil)
	if got := h.ItemInfo(script.IntVal(1101), itemdb.InfoBuy); got.Int != -1 {
		t.Errorf("Info with nil registry = %+v, want -1", got)
	}
}
