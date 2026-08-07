package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bouroo/goAthena/internal/modules/content/domain"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

// connWriter adapts a gateway domain.Conn (Write returns only error) to the
// io.Writer the kernel packet encoders target. Duplicated from infra and world/app:
// each module keeps its own copy so the gateway domain is the only cross-module
// dependency a feature module needs.
type connWriter struct{ conn gwdomain.Conn }

func (w connWriter) Write(p []byte) (int, error) {
	if err := w.conn.Write(p); err != nil {
		return 0, fmt.Errorf("write to conn: %w", err)
	}
	return len(p), nil
}

// scriptHost implements script.Host using the active connection and dialog channels.
// It translates VM builtin actions into the correct ZC frames and waits for CZ progression.
type scriptHost struct {
	npcID     uint32
	conn      gwdomain.Conn
	accountID uint32
	charID    uint32
	dialogCh  chan domain.DialogSignal
	world     domain.World
}

// NewScriptHost returns a script.Host for an active dialog session.
func NewScriptHost(npcID uint32, conn gwdomain.Conn, accountID, charID uint32, dialogCh chan domain.DialogSignal, world domain.World) script.Host {
	return &scriptHost{
		npcID:     npcID,
		conn:      conn,
		accountID: accountID,
		charID:    charID,
		dialogCh:  dialogCh,
		world:     world,
	}
}

// Mes sends the dialog line (ZC_SAY_DIALOG2); non-blocking because the VM moves
// on immediately.
func (h *scriptHost) Mes(msg string) {
	resp := packet.SayDialog2Response{
		NpcID:   h.npcID,
		Type:    0,
		Message: msg,
	}
	_ = resp.Encode(connWriter{h.conn})
}

// Next waits for the client to progress the dialog.
// A 30_000ms timer bounds the wait to prevent leaks if the client drops silently.
func (h *scriptHost) Next() bool {
	resp := packet.WaitDialog2Response{
		NpcID: h.npcID,
		Type:  0,
	}
	_ = resp.Encode(connWriter{h.conn})

	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()

	select {
	case sig := <-h.dialogCh:
		return sig.Advance
	case <-timer.C:
		return false
	}
}

// Select sends the menu option list (ZC_MENU_LIST) and blocks for the client's
// CZ_CHOOSE_MENU. rAthena's select numbers the colon-separated options 1..N and
// yields 255 (0xff) on cancel. A terminated dialog (advance=false, e.g. the
// player closed or the connection dropped) resolves as a cancel so the VM's
// select expression sees a value scripts already test for.
func (h *scriptHost) Select(options []string) int {
	resp := packet.MenuListResponse{
		NpcID: h.npcID,
		Items: strings.Join(options, ":"),
	}
	_ = resp.Encode(connWriter{h.conn})

	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()

	select {
	case sig := <-h.dialogCh:
		if !sig.Advance {
			return 255 // cancel: dialog closed/dropped
		}
		return int(sig.Choice)
	case <-timer.C:
		return 255 // cancel: client dropped silently
	}
}

// Input sends the numeric-input dialog (ZC_OPEN_EDITDLG) and blocks for the
// client's CZ_INPUT_EDITDLG. A 30s timer bounds the wait to prevent leaks if
// the client drops silently. Returns the entered amount and false if the dialog
// was closed/dropped/timed out, so builtinInput ends the script.
func (h *scriptHost) Input() (int64, bool) {
	resp := packet.OpenEditDlgResponse{NpcID: h.npcID}
	_ = resp.Encode(connWriter{h.conn})

	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()

	select {
	case sig := <-h.dialogCh:
		return int64(sig.Amount), sig.Advance
	case <-timer.C:
		return 0, false
	}
}

// InputStr sends the string-input dialog (ZC_OPEN_EDITDLGSTR) and blocks for the
// client's CZ_INPUT_EDITDLGSTR. A 30s timer bounds the wait. Returns the entered
// text and false if the dialog was closed/dropped/timed out.
func (h *scriptHost) InputStr() (string, bool) {
	resp := packet.OpenEditDlgStrResponse{NpcID: h.npcID}
	_ = resp.Encode(connWriter{h.conn})

	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()

	select {
	case sig := <-h.dialogCh:
		return sig.Text, sig.Advance
	case <-timer.C:
		return "", false
	}
}

// Close sends the close-dialog frame (ZC_CLOSE_DIALOG) to the client.
func (h *scriptHost) Close() {
	resp := packet.CloseDialogResponse{
		NpcID: h.npcID,
	}
	_ = resp.Encode(connWriter{h.conn})
}

// Warp delegates to the world module via the domain.World port; the result is
// discarded because it is fire-and-forget from the script VM's perspective.
func (h *scriptHost) Warp(mapName string, x, y int) {
	_ = h.world.Warp(context.Background(), h.conn, h.accountID, h.charID, mapName, x, y)
}

// PercentHeal applies an HP/SP percentage heal via the world module.
func (h *scriptHost) PercentHeal(hpPct, spPct int) {
	_ = h.world.Heal(context.Background(), h.conn, h.accountID, h.charID, hpPct, spPct)
}
