package app

import (
	"context"
	"fmt"

	"github.com/bouroo/goAthena/internal/modules/content/domain"
	"github.com/bouroo/goAthena/internal/modules/content/infra"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

// DI parameters.

type DialogService struct {
	world          domain.World
	dialogRegistry domain.DialogRegistry
	scripts        *ScriptStore
}

func NewDialogService(
	world domain.World,
	dialogRegistry domain.DialogRegistry,
	scripts *ScriptStore,
) *DialogService {
	return &DialogService{
		world:          world,
		dialogRegistry: dialogRegistry,
		scripts:        scripts,
	}
}

// ContactNPCHandler serves ZC_CONTACTNPC / CZ_CONTACTNPC.
type ContactNPCHandler struct {
	svc *DialogService
}

func NewContactNPCHandler(svc *DialogService) *ContactNPCHandler {
	return &ContactNPCHandler{svc: svc}
}

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
	host := infra.NewScriptHost(req.AID, conn, auth.AccountID, charID, ch, h.svc.world)
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

func NewReqNextScriptHandler(svc *DialogService) *ReqNextScriptHandler {
	return &ReqNextScriptHandler{svc: svc}
}

func (h *ReqNextScriptHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	if _, err := packet.ParseCZReqNextScript(frame.Raw); err != nil {
		return fmt.Errorf("parse CZ_REQ_NEXT_SCRIPT: %w", err)
	}
	auth := conn.Auth()
	if ch := h.svc.dialogRegistry.Get(auth.AccountID); ch != nil {
		// Non-blocking send: if the dialog already timed out, drop the advance.
		select {
		case ch <- true:
		default:
		}
	}
	return nil
}

// ChooseMenuHandler serves CZ_CHOOSE_MENU.
type ChooseMenuHandler struct {
	svc *DialogService
}

func NewChooseMenuHandler(svc *DialogService) *ChooseMenuHandler {
	return &ChooseMenuHandler{svc: svc}
}

func (h *ChooseMenuHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	if _, err := packet.ParseCZChooseMenu(frame.Raw); err != nil {
		return fmt.Errorf("parse CZ_CHOOSE_MENU: %w", err)
	}
	// TODO(M11d): menu/select handling.
	return nil
}

// CloseDialogHandler serves CZ_CLOSE_DIALOG.
type CloseDialogHandler struct {
	svc *DialogService
}

func NewCloseDialogHandler(svc *DialogService) *CloseDialogHandler {
	return &CloseDialogHandler{svc: svc}
}

func (h *CloseDialogHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	if _, err := packet.ParseCZCloseDialog(frame.Raw); err != nil {
		return fmt.Errorf("parse CZ_CLOSE_DIALOG: %w", err)
	}

	auth := conn.Auth()
	if ch := h.svc.dialogRegistry.Get(auth.AccountID); ch != nil {
		// Sending false breaks the dialog's Next() wait, ending it.
		select {
		case ch <- false:
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
	_ gwdomain.PacketHandler = (*CloseDialogHandler)(nil).Handle
)
