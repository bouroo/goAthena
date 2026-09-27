// Package app implements the content bounded context: it bridges the kernel's
// script VM to the game's dialog packets. An NPC click (CZ_CONTACT_NPC) starts
// a script; the VM runs in a goroutine and blocks in script.Host.Next/Select
// while the gateway's dialog-response handlers (CZ_REQ_NEXT_SCRIPT etc.) signal
// the per-player DialogSession to resume it.
package app

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bouroo/goAthena/internal/modules/content/domain"
	transitdomain "github.com/bouroo/goAthena/internal/modules/transit/domain"
	"github.com/bouroo/goAthena/internal/shared/safe"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

// dialogTimeout caps how long a Host call blocks for a client response before
// the dialog auto-cancels (matching rAthena's idle-dialog timeout behavior).
const dialogTimeout = 30 * time.Second

// Engine loads NPC scripts and runs them on click, coordinating dialog sessions.
type Engine struct {
	scripts   *script.CompiledScriptSet
	npcs      domain.NPCStore
	world     domain.ScriptWorld
	inventory domain.ScriptInventory
	quest     domain.ScriptQuest
	// items is the kernel item_db registry the getiteminfo builtin reads. nil
	// (no item_db loaded) makes every lookup an unknown item.
	items *itemdb.Registry
	log   *slog.Logger

	mu       sync.Mutex
	sessions map[uint32]*domain.DialogSession // key = accountID
}

// NewEngine builds an Engine from compiled scripts, an NPC store, the world
// port used by effect builtins (warp/heal/announce), and the inventory port used
// by item-script builtins (getitem/delitem/countitem/equip/unequip). scripts,
// world, and inventory may each be nil: clicks are then a no-op and effect
// builtins drop their frames / return 0 respectively. quest may be nil: the
// getvariableofnpc / setquestvar builtins then read 0 / silently no-op. items
// may be nil: getiteminfo then reports every item as unknown.
func NewEngine(scripts *script.CompiledScriptSet, npcs domain.NPCStore, world domain.ScriptWorld, inventory domain.ScriptInventory, quest domain.ScriptQuest, items *itemdb.Registry, log *slog.Logger) *Engine {
	return &Engine{scripts: scripts, npcs: npcs, world: world, inventory: inventory, quest: quest, items: items, log: log, sessions: make(map[uint32]*domain.DialogSession)}
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
	host := &ScriptHost{session: sess, world: e.world, inventory: e.inventory, quest: e.quest, items: e.items, log: e.log}
	// The VM runs scripts reached from the client (NPC clicks, dialog input), so
	// a panic inside it must cost this one player's dialog — runScript's own
	// defer still unregisters the session — rather than the process.
	safe.Go(e.log, "content.runScript", func() { e.runScript(accountID, cs, host) })
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
// via the session's Signal channel. Inventory effects (getitem/delitem/countitem/
// equip/unequip) flow through the injected ScriptInventory port — the engine
// passes a world-owned adapter; nil disables the item-script builtins (the
// builtin returns 0 and the script continues). items is the kernel item_db
// registry; nil reports every item as unknown to getiteminfo.
type ScriptHost struct {
	session   *domain.DialogSession
	world     domain.ScriptWorld
	inventory domain.ScriptInventory
	quest     domain.ScriptQuest
	items     *itemdb.Registry
	log       *slog.Logger
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
	var items strings.Builder
	for i, o := range options {
		if i > 0 {
			items.WriteString(":")
		}
		items.WriteString(o)
	}
	var buf bytes.Buffer
	_ = ropacket.MenuListResponse{NpcID: h.session.NpcID, Items: items.String()}.Encode(&buf)
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
	return h.waitInputStr()
}

// Close sends the close-dialog frame (ZC_CLOSE_DIALOG).
func (h *ScriptHost) Close() {
	var buf bytes.Buffer
	_ = ropacket.CloseDialogResponse{NpcID: h.session.NpcID}.Encode(&buf)
	h.session.Writer.WritePacket(buf.Bytes())
}

// Warp moves the player to the named map tile. The zone resolution (M12)
// picks the frame: a remote zone persists the destination, tears the player
// out of this zone's world (LeaveRemoteZone), and emits ZC_NPCACK_SERVERMOVE
// so the client reconnects to that zone's ip:port (rAthena
// clif_changemapserver); the local case is the pre-M12 behavior (persist via
// the world port + ZC_NPCACK_MAPMOVE). No-ops when no world is wired or the
// player is not on a map.
func (h *ScriptHost) Warp(mapName string, x, y int) {
	if h.world == nil {
		return
	}
	if zone, err := h.world.ResolveZone(mapName); err == nil && zone != (transitdomain.Zone{}) {
		// Persist the destination map+cell first (the remote zone's EnterMap
		// loads it), so the ScriptWorld port needs no second persist verb.
		if werr := h.world.WarpPlayer(h.session.CharID, mapName, int16(x), int16(y)); werr != nil { //nolint:gosec // G115: x/y are map-tile coords bounded by map dimensions.
			h.log.Debug("content: warp dropped (player not on map)", "charID", h.session.CharID, "map", mapName, "err", werr)
			return
		}
		if lerr := h.world.LeaveRemoteZone(context.Background(), h.session.CharID, int16(x), int16(y)); lerr != nil { //nolint:gosec // G115: x/y are map-tile coords bounded by map dimensions.
			h.log.Debug("content: cross-zone warp dropped (player not on map)", "charID", h.session.CharID, "map", mapName, "err", lerr)
			return
		}
		var sbuf bytes.Buffer
		_ = ropacket.ServerMoveResponse{MapName: mapName, X: uint16(x), Y: uint16(y), IP: zone.IPv4, Port: zone.Port}.Encode(&sbuf) //nolint:errcheck,gosec // G115: x/y are map-tile coords; map names are bounded by data.
		h.session.Writer.WritePacket(sbuf.Bytes())
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

// GetItem grants amount units of nameID. The world port handles the bag-side
// validation (weight/capacity) and emits the ZC_ITEM_PICKUP_ACK the client
// needs. No-op when no inventory port is wired.
func (h *ScriptHost) GetItem(nameID uint32, amount int) bool {
	if h.inventory == nil {
		return false
	}
	return h.inventory.GetItem(h.session.CharID, nameID, amount)
}

// DelItem removes amount units of nameID. The world port rejects partial
// removals (returns false when the player doesn't hold enough), matching
// rAthena's all-or-nothing delitem contract.
func (h *ScriptHost) DelItem(nameID uint32, amount int) bool {
	if h.inventory == nil {
		return false
	}
	return h.inventory.DelItem(h.session.CharID, nameID, amount)
}

// CountItem returns the player's count of nameID. A nil port reports zero.
func (h *ScriptHost) CountItem(nameID uint32) int {
	if h.inventory == nil {
		return 0
	}
	return h.inventory.CountItem(h.session.CharID, nameID)
}

// Equip equips the item at the given LoadByChar index to slot. The world port
// resolves index → row, applies the bitmask, and persists.
func (h *ScriptHost) Equip(index int, slot uint32) bool {
	if h.inventory == nil {
		return false
	}
	return h.inventory.Equip(h.session.CharID, index, slot)
}

// Unequip clears the equip bitmask of the item at the given index.
func (h *ScriptHost) Unequip(index int) bool {
	if h.inventory == nil {
		return false
	}
	return h.inventory.Unequip(h.session.CharID, index)
}

// GetQuestVar reads a persistent NPC-scoped variable for the dialog's player.
// Returns 0 when no quest port is wired or the variable is unset — matches
// rAthena's "unset integer reads as 0" (script.cpp get_val).
func (h *ScriptHost) GetQuestVar(npcName, varName string) int64 {
	if h.quest == nil {
		return 0
	}
	v := h.quest.GetVar(h.session.CharID, npcName, varName)
	return v
}

// SetQuestVar stores a persistent NPC-scoped variable for the dialog's
// player. Returns the wrapped error to the VM; a nil quest port returns
// nil (the script continues as if the write succeeded).
func (h *ScriptHost) SetQuestVar(npcName, varName string, value int64) error {
	if h.quest == nil {
		return nil
	}
	if err := h.quest.SetVar(h.session.CharID, npcName, varName, value); err != nil {
		h.log.Debug("content: quest set failed", "charID", h.session.CharID, "npc", npcName, "var", varName, "val", value, "err", err)
		return fmt.Errorf("quest set: %w", err)
	}
	return nil
}

// Announce broadcasts text to the audience the flag's BC_* target bits select
// (rAthena's buildin_announce, script.cpp:11957). The audience is resolved by
// the world module — it owns the entity registry and AOI grids — and the frames
// leave through the gateway's connection map. The anchor is the NPC when the
// flag carries BC_NPC and the dialog's player otherwise, matching rAthena's
// source selection. No-op when no world is wired.
func (h *ScriptHost) Announce(msg string, flag int) {
	if h.world == nil {
		return
	}
	h.world.Announce(h.announceAnchor(flag), msg, flag)
}

// AnnounceMap broadcasts text to every player on mapName — rAthena's
// mapannounce (script.cpp:12028), which ignores the flag's target bits.
func (h *ScriptHost) AnnounceMap(mapName, msg string, flag int) {
	if h.world == nil {
		return
	}
	h.world.AnnounceMap(mapName, msg, flag)
}

// announceAnchor picks the broadcast source: the NPC running the script when the
// flag sets BC_NPC, the dialog's player otherwise (script.cpp:11973).
func (h *ScriptHost) announceAnchor(flag int) uint32 {
	const bcNPC = 0x08
	if flag&bcNPC != 0 {
		return h.session.NpcID
	}
	return h.session.CharID
}

// HealAbs restores HP/SP by absolute amounts — the `heal` builtin
// (script.cpp:6007 status_heal). The world port clamps to the player's maxima
// and fires the vitals notification the gateway relays, so no frame is written
// here. No-op when no world is wired or the player is not on a map.
func (h *ScriptHost) HealAbs(hp, sp int) {
	if h.world == nil {
		return
	}
	if err := h.world.HealAbs(h.session.CharID, hp, sp); err != nil {
		h.log.Debug("content: heal dropped (player not on map)", "charID", h.session.CharID, "err", err)
	}
}

// ItemInfo answers getiteminfo (rAthena buildin_getiteminfo, script.cpp:14761)
// from the kernel item_db registry. The item argument is a numeric name id or an
// AegisName string — rAthena dispatches on the argument's type
// (script_isstring), so the distinction is carried into the lookup. An unknown
// item, an unsupported code, or an unloaded registry all yield -1;
// ITEMINFO_AEGISNAME answers with the item's AegisName string, the one string
// column in the set.
func (h *ScriptHost) ItemInfo(item script.Value, info int) script.Value {
	entry := h.lookupItem(item)
	if entry == nil {
		if info == itemdb.InfoAegisName {
			return script.StrVal("") // rAthena: an unknown item's AegisName is "" (script.cpp:14775)
		}
		return script.IntVal(-1)
	}
	if info == itemdb.InfoAegisName {
		return script.StrVal(entry.AegisName)
	}
	return script.IntVal(entry.Info(info))
}

// lookupItem resolves a getiteminfo item argument to its item_db entry, or nil
// when it names no known item.
func (h *ScriptHost) lookupItem(item script.Value) *itemdb.ItemEntry {
	if h.items == nil {
		return nil
	}
	// A string argument is an AegisName (rAthena's item_db.searchname path,
	// script.cpp:14767); a numeric one is a name id.
	if item.Kind == script.KindStr {
		return h.items.ByAegisName(item.Str)
	}
	n := item.Int
	if n < 0 || n > maxItemID {
		return nil
	}
	return h.items.Get(int32(n)) //nolint:gosec // G115: bounded by maxItemID above.
}

// maxItemID bounds the getiteminfo uint32→int32 cast for item_db lookup; item_db
// ids are < 2^31 (same bound the gateway's itemEntry uses).
const maxItemID = int64(1<<31 - 1)

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

// waitInputStr blocks for a string input. Close/timeout → "", false.
func (h *ScriptHost) waitInputStr() (string, bool) {
	t := time.NewTimer(dialogTimeout)
	defer t.Stop()
	select {
	case sig := <-h.session.Signal:
		if sig.Cancel {
			return "", false
		}
		return sig.Input, true
	case <-t.C:
		return "", false
	}
}
