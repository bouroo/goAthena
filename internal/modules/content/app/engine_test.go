//go:build unit

package app

import (
	"encoding/binary"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/bouroo/goAthena/internal/modules/content/domain"
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

// fakeScriptWorld is an in-memory ScriptWorld: it records effect calls and
// returns scripted HP/SP (and err) for heals. err, when set, makes both methods
// fail so the ScriptHost exercises its drop-frame branch.
type fakeScriptWorld struct {
	warps []warpCall
	heals []healCall
	hp    int32
	sp    int32
	err   error
}

func (f *fakeScriptWorld) WarpPlayer(charID uint32, mapName string, x, y int16) error {
	f.warps = append(f.warps, warpCall{charID: charID, mapName: mapName, x: x, y: y})
	return f.err
}

func (f *fakeScriptWorld) HealPlayer(charID uint32, hpPct, spPct int) (int32, int32, error) {
	f.heals = append(f.heals, healCall{charID: charID, hpPct: hpPct, spPct: spPct})
	return f.hp, f.sp, f.err
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

func newTestHost(world domain.ScriptWorld, charID uint32) (*ScriptHost, *captureWriter) {
	w := &captureWriter{}
	sess := &domain.DialogSession{CharID: charID, Writer: w, Signal: make(chan domain.DialogSignal, 1)}
	return &ScriptHost{session: sess, world: world, log: slog.Default()}, w
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
