package app

import (
	"bytes"
	"context"
	"fmt"

	"github.com/bouroo/goAthena/internal/modules/content/domain"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

// DialogService is the application-layer facade that handlers delegate to. It
// owns the per-NPC dialog state store, the script store, the shop-NPC index
// (used to route CZ_CONTACTNPC for shop NPCs to the deal-type selector
// instead of a script dialog), and the world port the dialog VMs use to
// mutate player state.
type DialogService struct {
	world          domain.World
	dialogRegistry domain.DialogRegistry
	scripts        *ScriptStore
	shopNPCs       map[uint32]bool
}

// NewDialogService wires the dependencies every dialog handler needs into a
// single facade. shopNPCs is the set of NPC EntityIDs that are shop NPCs;
// these short-circuit CZ_CONTACTNPC to ZC_SELECT_DEALTYPE rather than running
// a dialog script. A nil or empty map is acceptable: with no shop NPCs
// registered, every NPC follows the script flow.
func NewDialogService(
	world domain.World,
	dialogRegistry domain.DialogRegistry,
	scripts *ScriptStore,
	shopNPCs map[uint32]bool,
) *DialogService {
	return &DialogService{
		world:          world,
		dialogRegistry: dialogRegistry,
		scripts:        scripts,
		shopNPCs:       shopNPCs,
	}
}

// ContactNPCHandler serves ZC_CONTACTNPC / CZ_CONTACTNPC.
type ContactNPCHandler struct {
	svc *DialogService
}

// NewContactNPCHandler builds a CZ_CONTACTNPC handler bound to the DialogService.
func NewContactNPCHandler(svc *DialogService) *ContactNPCHandler {
	return &ContactNPCHandler{svc: svc}
}

// Handle starts a dialog session for the NPC the client clicked. Shop NPCs
// short-circuit to the Buy/Sell/Cancel deal-type selector (ZC_SELECT_DEALTYPE)
// instead of a script dialog; everything else runs its compiled script on
// its own goroutine, blocking on the dialog channel for Next/Close
// advancement. Unauthenticated connections (never passed the CZ_ENTER gate)
// drop the click silently.
func (h *ContactNPCHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCZContactNPC(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CZ_CONTACTNPC: %w", err)
	}

	auth := conn.Auth()
	if auth.AccountID == 0 {
		// Connection never passed the CZ_ENTER gate; the dialog is dropped but
		// the session stays open.
		return nil
	}

	// Shop NPCs bypass the script dialog entirely: the client pops up the
	// Buy/Sell/Cancel selector and the deal-type response (U4) drives the
	// rest of the shop flow. No dialog session is opened on this branch.
	if h.svc.shopNPCs != nil && h.svc.shopNPCs[req.AID] {
		var buf bytes.Buffer
		if err := (packet.SelectDealtypeResponse{NpcID: req.AID}).Encode(&buf); err != nil {
			return fmt.Errorf("encode ZC_SELECT_DEALTYPE: %w", err)
		}
		if err := conn.Write(buf.Bytes()); err != nil {
			return fmt.Errorf("send ZC_SELECT_DEALTYPE: %w", err)
		}
		return nil
	}

	// Make sure they don't already have an active dialog.
	ch, err := h.svc.dialogRegistry.Open(auth.AccountID)
	if err != nil {
		return fmt.Errorf("dialog: open session for account %d: %w", auth.AccountID, err)
	}

	// Resolve the clicked NPC by its EntityID. NPC ids are allocated from
	// NPCIDBase (500000000) upward at boot, in the same order the script store
	// recorded them, so the index is the id offset from the base.
	npcIndex := req.AID - worlddomain.NPCIDBase
	if req.AID < worlddomain.NPCIDBase || int(npcIndex) >= len(h.svc.scripts.NPCs) {
		h.svc.dialogRegistry.Close(auth.AccountID)
		return nil
	}
	npc := h.svc.scripts.NPCs[npcIndex]

	// Find the compiled script for this NPC name.
	cs, ok := h.svc.scripts.Set.Scripts[npc.Name]
	if !ok {
		h.svc.dialogRegistry.Close(auth.AccountID)
		return nil
	}

	// ConnAuth carries the account but not the character id; the world player
	// session owns charID. For M11c (dialog/heal/warp MVP) we pass 0 — Heal/Warp
	// resolve the live char from the conn's world session when needed.
	var charID uint32
	host := NewScriptHost(req.AID, conn, auth.AccountID, charID, ch, h.svc.world)
	vars := map[string]script.Value{} // Fresh state per dialog session
	vm := script.NewVM(cs, host, script.DefaultBuiltins(), vars)

	// Run the script on its own goroutine; it blocks on the dialog channel
	// (Next/Close) and self-closes the session when it returns.
	go func() {
		defer h.svc.dialogRegistry.Close(auth.AccountID)
		_ = vm.Run()
	}()
	return nil
}

// ReqNextScriptHandler serves CZ_REQ_NEXT_SCRIPT.
type ReqNextScriptHandler struct {
	svc *DialogService
}

// NewReqNextScriptHandler builds a CZ_REQ_NEXT_SCRIPT handler bound to the
// DialogService.
func NewReqNextScriptHandler(svc *DialogService) *ReqNextScriptHandler {
	return &ReqNextScriptHandler{svc: svc}
}

// Handle advances the active dialog when the client presses Next; non-blocking
// on the dialog channel (a timed-out dialog drops the click).
func (h *ReqNextScriptHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	if _, err := packet.ParseCZReqNextScript(frame.Raw); err != nil {
		return fmt.Errorf("parse CZ_REQ_NEXT_SCRIPT: %w", err)
	}
	auth := conn.Auth()
	if ch := h.svc.dialogRegistry.Get(auth.AccountID); ch != nil {
		// Non-blocking send: if the dialog already timed out, drop the advance.
		select {
		case ch <- domain.DialogSignal{Advance: true}:
		default:
		}
	}
	return nil
}

// ChooseMenuHandler serves CZ_CHOOSE_MENU.
type ChooseMenuHandler struct {
	svc *DialogService
}

// NewChooseMenuHandler builds a CZ_CHOOSE_MENU handler bound to the DialogService.
func NewChooseMenuHandler(svc *DialogService) *ChooseMenuHandler {
	return &ChooseMenuHandler{svc: svc}
}

// Handle forwards the client's menu choice to the paused script. The 7-byte
// CZ_CHOOSE_MENU frame carries the NPC id and a choice byte (1..254 = option,
// 255 = cancel). The parser stores the choice as int8, so byte(int8) recovers
// both the cancel sentinel (0xff) and the 1-based option bytes unchanged. The
// choice rides the same DialogSignal the next/close handlers use, waking the
// scriptHost.Select() that emitted ZC_MENU_LIST (clif.cpp:13337
// clif_parse_NpcSelectMenu → npc_scriptcont). Non-blocking: a timed-out dialog
// drops the choice.
func (h *ChooseMenuHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCZChooseMenu(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CZ_CHOOSE_MENU: %w", err)
	}
	auth := conn.Auth()
	if ch := h.svc.dialogRegistry.Get(auth.AccountID); ch != nil {
		select {
		case ch <- domain.DialogSignal{Advance: true, Choice: byte(req.Selected)}: //nolint:gosec // G115: the parser stores the 0x00b8 choice byte as int8; byte(int8) is an intentional two's-complement recovery that reproduces 1..254 (choice) and 0xff (cancel) exactly
		default:
		}
	}
	return nil
}

// InputEditDlgHandler serves CZ_INPUT_EDITDLG (0x0143).
type InputEditDlgHandler struct {
	svc *DialogService
}

// NewInputEditDlgHandler builds a CZ_INPUT_EDITDLG handler bound to the
// DialogService.
func NewInputEditDlgHandler(svc *DialogService) *InputEditDlgHandler {
	return &InputEditDlgHandler{svc: svc}
}

// Handle forwards the client's numeric input to the paused script. The 10-byte
// CZ_INPUT_EDITDLG frame carries the NPC id and an int32 value. The value rides
// the DialogSignal (Amount), waking the scriptHost.Input() that emitted
// ZC_OPEN_EDITDLG (clif.cpp:13378 clif_parse_NpcAmountInput → npc_scriptcont).
// Non-blocking: a timed-out dialog drops the value.
func (h *InputEditDlgHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCZInputEditDlg(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CZ_INPUT_EDITDLG: %w", err)
	}
	auth := conn.Auth()
	if ch := h.svc.dialogRegistry.Get(auth.AccountID); ch != nil {
		select {
		case ch <- domain.DialogSignal{Advance: true, Amount: req.Value}:
		default:
		}
	}
	return nil
}

// InputEditDlgStrHandler serves CZ_INPUT_EDITDLGSTR (0x01d5).
type InputEditDlgStrHandler struct {
	svc *DialogService
}

// NewInputEditDlgStrHandler builds a CZ_INPUT_EDITDLGSTR handler bound to the
// DialogService.
func NewInputEditDlgStrHandler(svc *DialogService) *InputEditDlgStrHandler {
	return &InputEditDlgStrHandler{svc: svc}
}

// Handle forwards the client's string input to the paused script. The variable-
// length CZ_INPUT_EDITDLGSTR frame carries the NPC id and the text. The text
// rides the DialogSignal (Text), waking the scriptHost.InputStr() that emitted
// ZC_OPEN_EDITDLGSTR (clif.cpp:13397 clif_parse_NpcStringInput → npc_scriptcont).
// Non-blocking: a timed-out dialog drops the value.
func (h *InputEditDlgStrHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCZInputEditDlgStr(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CZ_INPUT_EDITDLGSTR: %w", err)
	}
	auth := conn.Auth()
	if ch := h.svc.dialogRegistry.Get(auth.AccountID); ch != nil {
		select {
		case ch <- domain.DialogSignal{Advance: true, Text: req.Value}:
		default:
		}
	}
	return nil
}

// CloseDialogHandler serves CZ_CLOSE_DIALOG.
type CloseDialogHandler struct {
	svc *DialogService
}

// NewCloseDialogHandler builds a CZ_CLOSE_DIALOG handler bound to the DialogService.
func NewCloseDialogHandler(svc *DialogService) *CloseDialogHandler {
	return &CloseDialogHandler{svc: svc}
}

// Handle ends the active dialog by signalling false on the dialog channel;
// the VM goroutine wakes from Next and falls through to its deferred Close.
func (h *CloseDialogHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	if _, err := packet.ParseCZCloseDialog(frame.Raw); err != nil {
		return fmt.Errorf("parse CZ_CLOSE_DIALOG: %w", err)
	}

	auth := conn.Auth()
	if ch := h.svc.dialogRegistry.Get(auth.AccountID); ch != nil {
		// Sending Advance=false breaks the dialog's Next()/Select() wait,
		// ending it; select() resolves the close as a cancel.
		select {
		case ch <- domain.DialogSignal{Advance: false}:
		default:
		}
	}
	return nil
}

// Compile-time checks that the handlers satisfy the gateway handler shape.
var (
	_ gwdomain.PacketHandler = (*ContactNPCHandler)(nil).Handle
	_ gwdomain.PacketHandler = (*ReqNextScriptHandler)(nil).Handle
	_ gwdomain.PacketHandler = (*ChooseMenuHandler)(nil).Handle
	_ gwdomain.PacketHandler = (*InputEditDlgHandler)(nil).Handle
	_ gwdomain.PacketHandler = (*InputEditDlgStrHandler)(nil).Handle
	_ gwdomain.PacketHandler = (*CloseDialogHandler)(nil).Handle
)
