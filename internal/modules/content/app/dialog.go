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

func (h *ContactNPCHandler) Handle(ctx context.Context, conn gwdomain.Conn, payload []byte) {
	req, err := packet.ParseCZContactNPC(payload)
	if err != nil {
		fmt.Printf("CZ_CONTACTNPC parse err: %v\n", err) // TODO: use logger
		return
	}

	auth := conn.Auth()
	if !auth.Authenticated {
		return
	}

	// Make sure they don't already have an active dialog.
	ch, err := h.svc.dialogRegistry.Open(auth.AccountID)
	if err != nil {
		fmt.Printf("dialogRegistry.Open: %v\n", err)
		return
	}

	// Ensure the NPC exists in our store (using NA for Map/X/Y right now since this is contact).
	// EntityNPC is NpcIDBase (500000000) offset inside AOI. For now just search the array?
	// Real implementation uses a registry. For MVP, we need to know what script to run.
	// Since rAthena client expects NpcID = NPC id we assigned (e.g. 500000000 + idx):
	npcIndex := req.NpcID - worlddomain.NPCIDBase
	if req.NpcID < worlddomain.NPCIDBase || int(npcIndex) >= len(h.svc.scripts.NPCs) {
		// Not a known NPC
		h.svc.dialogRegistry.Close(auth.AccountID)
		return
	}
	npc := h.svc.scripts.NPCs[npcIndex]

	// Find the compiled script for this NPC name
	cs, ok := h.svc.scripts.Set.Scripts[npc.Name]
	if !ok {
		h.svc.dialogRegistry.Close(auth.AccountID)
		return
	}

	// Create Host and VM
	host := infra.NewScriptHost(req.NpcID, conn, auth.AccountID, auth.CharID, ch, h.svc.world)
	vars := map[string]script.Value{} // Fresh state per dialog session
	vm := script.NewVM(cs, host, script.DefaultBuiltins(), vars)

	// Dispatch run in goroutine
	go func() {
		defer h.svc.dialogRegistry.Close(auth.AccountID)
		_ = vm.Run() // Exec
	}()
}

// ReqNextScriptHandler serves CZ_REQ_NEXT_SCRIPT.
type ReqNextScriptHandler struct {
	svc *DialogService
}

func NewReqNextScriptHandler(svc *DialogService) *ReqNextScriptHandler {
	return &ReqNextScriptHandler{svc: svc}
}

func (h *ReqNextScriptHandler) Handle(ctx context.Context, conn gwdomain.Conn, payload []byte) {
	_, err := packet.ParseCZReqNextScript(payload)
	if err != nil {
		return
	}
	auth := conn.Auth()
	if ch := h.svc.dialogRegistry.Get(auth.AccountID); ch != nil {
		// Non-blocking send: if the timer expired right as we receive this, skip it.
		// A well behaved client won't overload this.
		select {
		case ch <- true:
		default:
		}
	}
}

// ChooseMenuHandler serves CZ_CHOOSE_MENU.
type ChooseMenuHandler struct {
	svc *DialogService
}

func NewChooseMenuHandler(svc *DialogService) *ChooseMenuHandler {
	return &ChooseMenuHandler{svc: svc}
}

func (h *ChooseMenuHandler) Handle(ctx context.Context, conn gwdomain.Conn, payload []byte) {
	_, err := packet.ParseCZChooseMenu(payload)
	if err != nil {
		return
	}
	// TODO(M11d): Write choosemenu handling when implementing `menu`/`select`
}

// CloseDialogHandler serves CZ_CLOSE_DIALOG.
type CloseDialogHandler struct {
	svc *DialogService
}

func NewCloseDialogHandler(svc *DialogService) *CloseDialogHandler {
	return &CloseDialogHandler{svc: svc}
}

func (h *CloseDialogHandler) Handle(ctx context.Context, conn gwdomain.Conn, payload []byte) {
	_, err := packet.ParseCZCloseDialog(payload)
	if err != nil {
		return
	}

	auth := conn.Auth()
	if ch := h.svc.dialogRegistry.Get(auth.AccountID); ch != nil {
		// Close via sending false, which breaks Next().
		select {
		case ch <- false:
		default:
		}
	}
}

// worlddomain placeholder for NPCIDBase (avoids circular dependency on worlddomain if we redefine it).
// But we *can* import worlddomain, architecture allows it (domain import). Let's use it directly.
