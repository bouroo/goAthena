// Package app implements the content bounded context: it bridges the kernel's
// script VM to the game's dialog packets. An NPC click (CZ_CONTACT_NPC) starts
// a script; the VM runs in a goroutine and blocks in script.Host.Next/Select
// while the gateway's dialog-response handlers (CZ_REQ_NEXT_SCRIPT etc.) signal
// the per-player DialogSession to resume it.
package app

import (
	"bytes"
	"context"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/bouroo/goAthena/internal/modules/content/domain"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

// dialogTimeout caps how long a Host call blocks for a client response before
// the dialog auto-cancels (matching rAthena's idle-dialog timeout behavior).
const dialogTimeout = 30 * time.Second

// Engine loads NPC scripts and runs them on click, coordinating dialog sessions.
type Engine struct {
	scripts *script.CompiledScriptSet
	npcs    domain.NPCStore
	world   domain.ScriptWorld
	log     *slog.Logger

	mu       sync.Mutex
	sessions map[uint32]*domain.DialogSession // key = accountID
}

// NewEngine builds an Engine from compiled scripts, an NPC store, and the world
// port used by effect builtins (warp/heal). scripts and world may be nil: clicks
// are then a no-op and effect builtins drop their frames.
func NewEngine(scripts *script.CompiledScriptSet, npcs domain.NPCStore, world domain.ScriptWorld, log *slog.Logger) *Engine {
	return &Engine{scripts: scripts, npcs: npcs, world: world, log: log, sessions: make(map[uint32]*domain.DialogSession)}
}

// StartDialog resolves the NPC's script, creates a dialog session, and runs the
// VM in a goroutine. Called from the CZ_CONTACT_NPC handler. No-op if the NPC
// has no script or no scripts are loaded.
func (e *Engine) StartDialog(accountID, charID, npcGID uint32, writer domain.PacketWriter) {
	if e.scripts == nil || e.npcs == nil {
		return
	}
	name, ok := e.npcs.ScriptForNPC(context.Background(), npcGID)
	if !ok {
		e.log.Debug("content: NPC has no script", "npcGID", npcGID)
		return
	}
	cs, ok := e.scripts.Scripts[name]
	if !ok {
		e.log.Debug("content: script not found", "name", name, "npcGID", npcGID)
		return
	}
	if e.active(accountID) {
		e.log.Debug("content: dialog already active", "accountID", accountID)
		return
	}
	sess := &domain.DialogSession{NpcID: npcGID, CharID: charID, Writer: writer, Signal: make(chan domain.DialogSignal, 1)}
	e.put(accountID, sess)
	host := &ScriptHost{session: sess, world: e.world, log: e.log}
	go e.runScript(accountID, cs, host)
}

// runScript runs the VM and always unregisters the session on completion.
func (e *Engine) runScript(accountID uint32, cs *script.CompiledScript, host *ScriptHost) {
	defer e.end(accountID)
	vm := script.NewVM(cs, host, script.DefaultBuiltins(), map[string]script.Value{})
	if err := vm.Run(); err != nil {
		e.log.Warn("content: script run", "accountID", accountID, "err", err)
	}
}

// Signal delivers a dialog response to the player's active session. No-op if the
// player has no active dialog (e.g. a late packet after close).
func (e *Engine) Signal(accountID uint32, sig domain.DialogSignal) {
	sess := e.get(accountID)
	if sess == nil {
		return
	}
	select {
	case sess.Signal <- sig:
	default: // session already has a pending signal; drop the duplicate
	}
}

// EndDialog forcibly ends a player's dialog (e.g. on disconnect).
func (e *Engine) EndDialog(accountID uint32) {
	e.end(accountID)
}

func (e *Engine) active(accountID uint32) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.sessions[accountID]
	return ok
}

func (e *Engine) put(accountID uint32, s *domain.DialogSession) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sessions[accountID] = s
}

func (e *Engine) get(accountID uint32) *domain.DialogSession {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sessions[accountID]
}

func (e *Engine) end(accountID uint32) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.sessions, accountID)
}

// ScriptHost implements script.Host, bridging the VM's blocking dialog calls to
// the game's ZC_/CZ_ dialog packets. Blocking calls receive the client's reply
// via the session's Signal channel.
type ScriptHost struct {
	session *domain.DialogSession
	world   domain.ScriptWorld
	log     *slog.Logger
}

// Mes sends a ZC_SAY_DIALOG2 dialog line. Non-blocking.
func (h *ScriptHost) Mes(msg string) {
	var buf bytes.Buffer
	_ = ropacket.SayDialog2Response{NpcID: h.session.NpcID, Message: msg}.Encode(&buf) //nolint:errcheck // kernel encoders error only on oversized strings.
	h.session.Writer.WritePacket(buf.Bytes())
}

// Next sends the "click Next" prompt (ZC_WAIT_DIALOG2) and blocks until the
// client advances. Returns false on close/disconnect/timeout.
func (h *ScriptHost) Next() bool {
	var buf bytes.Buffer
	_ = ropacket.WaitDialog2Response{NpcID: h.session.NpcID}.Encode(&buf)
	h.session.Writer.WritePacket(buf.Bytes())
	return h.waitAdvance()
}

// Select sends the menu option list (ZC_MENU_LIST) and blocks until the client
// chooses. Returns the 1-based index, or 255 for cancel.
func (h *ScriptHost) Select(options []string) int {
	items := ""
	for i, o := range options {
		if i > 0 {
			items += ":"
		}
		items += o
	}
	var buf bytes.Buffer
	_ = ropacket.MenuListResponse{NpcID: h.session.NpcID, Items: items}.Encode(&buf)
	h.session.Writer.WritePacket(buf.Bytes())
	return h.waitChoice()
}

// Input sends the numeric-input dialog (ZC_OPEN_EDITDLG) and blocks until the
// client replies. Returns the entered amount, or 0+false on close/timeout.
func (h *ScriptHost) Input() (int64, bool) {
	var buf bytes.Buffer
	_ = ropacket.OpenEditDlgResponse{NpcID: h.session.NpcID}.Encode(&buf)
	h.session.Writer.WritePacket(buf.Bytes())
	return h.waitInput()
}

// InputStr sends the string-input dialog (ZC_OPEN_EDITDLGSTR) and blocks until
// the client replies. Returns the entered text, or ""+false on close/timeout.
func (h *ScriptHost) InputStr() (string, bool) {
	var buf bytes.Buffer
	_ = ropacket.OpenEditDlgStrResponse{NpcID: h.session.NpcID}.Encode(&buf)
	h.session.Writer.WritePacket(buf.Bytes())
	in, ok := h.waitInput()
	return strconv.FormatInt(in, 10), ok // numeric-encode stub; full str path in M10b
}

// Close sends the close-dialog frame (ZC_CLOSE_DIALOG).
func (h *ScriptHost) Close() {
	var buf bytes.Buffer
	_ = ropacket.CloseDialogResponse{NpcID: h.session.NpcID}.Encode(&buf)
	h.session.Writer.WritePacket(buf.Bytes())
}

// Warp moves the player to the named map tile: it persists the destination via
// the world port and emits ZC_NPCACK_MAPMOVE so the client reconnects there.
// No-ops when no world is wired or the player is not on a map.
func (h *ScriptHost) Warp(mapName string, x, y int) {
	if h.world == nil {
		return
	}
	if err := h.world.WarpPlayer(h.session.CharID, mapName, int16(x), int16(y)); err != nil { //nolint:gosec // G115: x/y are map-tile coords bounded by map dimensions.
		h.log.Debug("content: warp dropped (player not on map)", "charID", h.session.CharID, "map", mapName, "err", err)
		return
	}
	var buf bytes.Buffer
	_ = ropacket.MapMoveResponse{MapName: mapName, X: uint16(x), Y: uint16(y)}.Encode(&buf) //nolint:errcheck,gosec // G115: x/y are map-tile coords; map names are bounded by data.
	h.session.Writer.WritePacket(buf.Bytes())
}

// PercentHeal restores HP/SP by the given percentages of the player's maximums
// via the world port, then emits a ZC_PAR_CHANGE per vital with the new totals.
// No-ops when no world is wired or the player is not on a map.
func (h *ScriptHost) PercentHeal(hpPct, spPct int) {
	if h.world == nil {
		return
	}
	hp, sp, err := h.world.HealPlayer(h.session.CharID, hpPct, spPct)
	if err != nil {
		h.log.Debug("content: heal dropped (player not on map)", "charID", h.session.CharID, "err", err)
		return
	}
	var buf bytes.Buffer
	_ = ropacket.ParChangeResponse{VarID: ropacket.SPHP, Count: hp}.Encode(&buf)
	_ = ropacket.ParChangeResponse{VarID: ropacket.SPSP, Count: sp}.Encode(&buf)
	h.session.Writer.WritePacket(buf.Bytes())
}

// waitAdvance blocks for a Next/OK signal. Cancel/close/timeout → false.
func (h *ScriptHost) waitAdvance() bool {
	t := time.NewTimer(dialogTimeout)
	defer t.Stop()
	select {
	case sig := <-h.session.Signal:
		return sig.Advance && !sig.Cancel
	case <-t.C:
		return false
	}
}

// waitChoice blocks for a menu selection. Cancel/close/timeout → 255.
func (h *ScriptHost) waitChoice() int {
	t := time.NewTimer(dialogTimeout)
	defer t.Stop()
	select {
	case sig := <-h.session.Signal:
		if sig.Cancel {
			return 255
		}
		return int(sig.Choice)
	case <-t.C:
		return 255
	}
}

// waitInput blocks for a numeric input. Close/timeout → 0, false.
func (h *ScriptHost) waitInput() (int64, bool) {
	t := time.NewTimer(dialogTimeout)
	defer t.Stop()
	select {
	case sig := <-h.session.Signal:
		if sig.Cancel {
			return 0, false
		}
		n, err := strconv.ParseInt(sig.Input, 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	case <-t.C:
		return 0, false
	}
}
