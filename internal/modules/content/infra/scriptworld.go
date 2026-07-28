package infra

import (
	"context"
	"fmt"
	"time"

	"github.com/bouroo/goAthena/internal/modules/content/domain"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

// connWriter adapts a gateway domain.Conn (Write returns only error) to the
// io.Writer the kernel packet encoders target. Duplicated from world/app: each
// module keeps its own copy so the gateway domain is the only cross-module
// dependency a feature module needs.
type connWriter struct{ conn gwdomain.Conn }

func (w connWriter) Write(p []byte) (int, error) {
	if err := w.conn.Write(p); err != nil {
		return 0, fmt.Errorf("write to conn: %w", err)
	}
	return len(p), nil
}

// scriptWorld implements the domain.World interface used by builtins to effect changes
// upon the player/world, wrapping the underlying worlddomain registries.
type scriptWorld struct {
	players *worlddomain.PlayerRegistry
}

// NewScriptWorld constructs the domain.World interface implementation using the
// cross-module worlddomain.PlayerRegistry.
func NewScriptWorld(players *worlddomain.PlayerRegistry) domain.World {
	return &scriptWorld{players: players}
}

// Warp implements domain.World.Warp, updating character position and triggering
// the map change routine handled by the world module (delegated via player mutators).
func (w *scriptWorld) Warp(ctx context.Context, conn gwdomain.Conn, accountID, charID uint32, mapName string, x, y int) error {
	// TODO(M11d): implement warp (cross-map involves unregistering from AOI grid,
	// sending map-change packets, and re-entering similar to the map_enter logic).
	// For now, this is a stub since M11c is only the dialog subset context.
	return nil
}

// Heal implements domain.World.Heal, updating the player's HP/SP by percentage
// and broadcasting ZC_PAR_CHANGE.
func (w *scriptWorld) Heal(ctx context.Context, conn gwdomain.Conn, accountID, charID uint32, hpPct, spPct int) error {
	player, ok := w.players.ByAccount(accountID)
	if !ok {
		return fmt.Errorf("heal: player not found for account %d", accountID)
	}

	player.HP = (player.MaxHP * uint32(hpPct)) / 100 //nolint:gosec // G115: HP/SP percentages are bounded 0–100 game values
	player.SP = (player.MaxSP * uint32(spPct)) / 100 //nolint:gosec // G115: HP/SP percentages are bounded 0–100 game values

	// Note: in RAthena, clipping happens if > max
	if player.HP > player.MaxHP {
		player.HP = player.MaxHP
	}
	if player.SP > player.MaxSP {
		player.SP = player.MaxSP
	}

	cw := connWriter{conn: conn}

	if err := (packet.ParChangeResponse{VarID: packet.SPHP, Count: int32(player.HP)}).Encode(cw); err != nil { //nolint:gosec // G115: HP/SP are bounded after Max-clip above
		return fmt.Errorf("encode ZC_PAR_CHANGE SPHP: %w", err)
	}
	if err := (packet.ParChangeResponse{VarID: packet.SPSP, Count: int32(player.SP)}).Encode(cw); err != nil { //nolint:gosec // G115: HP/SP are bounded after Max-clip above
		return fmt.Errorf("encode ZC_PAR_CHANGE SPSP: %w", err)
	}

	return nil
}

// scriptHost implements script.Host using the active connection and dialog channels.
// It translates VM builtin actions into the correct ZC frames and waits for CZ progression.
type scriptHost struct {
	npcID     uint32
	conn      gwdomain.Conn
	accountID uint32
	charID    uint32
	dialogCh  chan bool
	world     domain.World
}

// NewScriptHost returns a script.Host for an active dialog session.
func NewScriptHost(npcID uint32, conn gwdomain.Conn, accountID, charID uint32, dialogCh chan bool, world domain.World) script.Host {
	return &scriptHost{
		npcID:     npcID,
		conn:      conn,
		accountID: accountID,
		charID:    charID,
		dialogCh:  dialogCh,
		world:     world,
	}
}

func (h *scriptHost) Mes(msg string) {
	resp := packet.SayDialog2Response{
		NpcID:   h.npcID,
		Type:    0,
		Message: msg,
	}
	_ = resp.Encode(connWriter{h.conn})
}

// Next waits for the client to progress the dialog.
// A 30_000ms timer bounds the wait preventing leaks if the client drops silently.
func (h *scriptHost) Next() bool {
	resp := packet.WaitDialog2Response{
		NpcID: h.npcID,
		Type:  0,
	}
	_ = resp.Encode(connWriter{h.conn})

	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()

	select {
	case advanced := <-h.dialogCh:
		return advanced
	case <-timer.C:
		return false
	}
}

func (h *scriptHost) Close() {
	resp := packet.CloseDialogResponse{
		NpcID: h.npcID,
	}
	_ = resp.Encode(connWriter{h.conn})
}

func (h *scriptHost) Warp(mapName string, x, y int) {
	_ = h.world.Warp(context.Background(), h.conn, h.accountID, h.charID, mapName, x, y)
}

func (h *scriptHost) PercentHeal(hpPct, spPct int) {
	_ = h.world.Heal(context.Background(), h.conn, h.accountID, h.charID, hpPct, spPct)
}
