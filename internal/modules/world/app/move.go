// Package app: this file adds the M4c movement use case. A CZ_REQUEST_MOVE
// (0x0085) from a client asks the server to walk the player's PC to a target
// cell. The server resolves a path with the map's A* pathfinder, then emits a
// self-ack (ZC_NOTIFY_PLAYERMOVE) to the mover and a walk broadcast
// (ZC_UNIT_WALKING) to AOI observers, and moves the AOI entity.
//
// The pathfinder is single-goroutine-unsafe by design (it mutates per-search
// scratch buffers; see pkg/ro/pathfinding.Pathfinder). The Map domain contract
// (world/domain/map.go) states that connection handlers must not call FindPath
// directly but enqueue movement for the tick to resolve. This file honors that
// contract: RequestMove enqueues a moveReq onto a channel, and a single worker
// goroutine (Run) owns every map's pathfinder and resolves moves in arrival
// order. One owner serializes FindPath, so two players moving on the same map
// at once cannot race the shared scratch buffers.
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/pathfinding"
)

// Clock is the monotone-tick source the worker stamps a move's MoveStartTime
// with. rAthena uses gettick() (monotone milliseconds); the real server
// supplies a clock backed by time.Now()/UnixMilli. Unit tests inject a
// deterministic clock so the self-ack's MoveStartTime is byte-predictable.
type Clock interface {
	// MoveStart returns the server's monotone tick in milliseconds — the value
	// the kernel writes into ZC_NOTIFY_PLAYERMOVE/ZC_UNIT_WALKING's
	// moveStartTime field. It is monotone (a later move always stamps a value
	// >= an earlier move's) so observer interpolation never runs backwards.
	MoveStart() uint32
}

// systemClock is the production Clock: monotone milliseconds since the Unix
// epoch, truncated to the uint32 wire slot. Overflow wraps every ~49.7 days,
// matching rAthena's gettick() uint32 behavior — the client treats the value
// as a relative interpolation anchor, not an absolute time.
type systemClock struct{}

// MoveStart implements Clock.
func (systemClock) MoveStart() uint32 {
	return uint32(time.Now().UnixMilli()) //nolint:gosec // G115: int64 ms → uint32 wire slot, rAthena gettick() wraps identically
}

// SystemClock returns the production Clock backed by wall-clock milliseconds.
// The composition root uses this for the live server; unit tests inject a
// deterministic clock instead.
func SystemClock() Clock { return systemClock{} }

// MoveService owns the movement use case and the single worker goroutine that
// resolves moves. It holds the live-session registry and the map store (to
// load the pathfinder for the player's current map) and nothing else. The
// worker is started by Run (registered as a Runnable in the composition root)
// and stopped when that context is cancelled; RequestMove is the conn-handler
// entry point that enqueues without blocking on the worker.
type MoveService struct {
	registry *domain.PlayerRegistry
	maps     domain.MapStore
	clock    Clock

	queue chan moveReq
}

// moveReq is a single enqueued move. accountID is the verified identity (sourced
// from conn.Auth().AccountID by the handler, never the packet), and destX/destY
// is the target cell the client clicked. The worker resolves the path from the
// player's current cell to dest.
type moveReq struct {
	accountID uint32
	destX     int16
	destY     int16
}

// NewMoveService binds the movement collaborators. queueSize bounds the
// backlog of pending moves; a full queue means the client is clicking faster
// than the worker drains and the oldest pending move is dropped (the client
// re-issues on the next click), so a slow worker cannot OOM the server.
func NewMoveService(registry *domain.PlayerRegistry, maps domain.MapStore, clock Clock, queueSize int) *MoveService {
	if queueSize < 1 {
		queueSize = 1
	}
	return &MoveService{
		registry: registry,
		maps:     maps,
		clock:    clock,
		queue:    make(chan moveReq, queueSize),
	}
}

// RequestMove is the CZ_REQUEST_MOVE handler entry point. It enqueues the move
// for the worker to resolve and returns immediately — the conn handler must
// not call FindPath (single-goroutine pathfinder contract) and must not block
// the dispatch loop on a slow pathfind. accountID MUST come from the verified
// conn auth cache, not the packet; the handler enforces this. A full queue is
// a backpressure signal, not an error: the move is dropped and nil returned so
// the connection stays open (rAthena silently ignores a move it cannot serve).
func (s *MoveService) RequestMove(_ context.Context, _ gwdomain.Conn, accountID uint32, req packet.CZRequestMoveRequest) error {
	select {
	case s.queue <- moveReq{accountID: accountID, destX: req.DestX, destY: req.DestY}:
	default:
		// Queue full: drop this move. The client re-issues on the next click;
		// a dropped move is a no-op, not a disconnect.
	}
	return nil
}

// Run is the movement worker. It owns every map's pathfinder: it is the sole
// goroutine that calls FindPath, so the pathfinder's mutable scratch buffers
// are never raced. It drains the queue until ctx is cancelled, then returns
// nil. Register this as a Runnable in the composition root so SIGTERM reaches
// it; a non-nil return is reserved for an unrecoverable fault (none here —
// every move is resolved or ignored, never propagated, matching rAthena's
// tolerant move handling).
func (s *MoveService) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case req := <-s.queue:
			s.resolve(ctx, req)
		}
	}
}

// resolve services one move: load the player, load its map, pathfind from the
// current cell to the target, and on success emit the self-ack + observer
// broadcast and move the AOI entity. A pathfind failure (no path, OOB, or
// unwalkable target) is ignored — rAthena drops the move silently rather than
// disconnecting or warping the player — so the player simply does not move.
func (s *MoveService) resolve(ctx context.Context, req moveReq) {
	player, ok := s.registry.ByAccount(req.accountID)
	if !ok {
		// A move from a player not in the registry is a late packet after
		// disconnect; nothing to move.
		return
	}

	mp, err := s.maps.Load(ctx, player.MapName)
	if err != nil {
		// A map that loaded at enter-world should still load; a failure here is
		// transient (the FileMapStore caches, so this is effectively never).
		// Drop the move rather than tearing down the session.
		return
	}
	if mp.Pathfinder == nil {
		// No pathfinder ⇒ no walkability model (a test map without .gat data).
		// Cannot pathfind; drop the move.
		return
	}

	srcX, srcY, _ := player.Position()
	start := pathfinding.Point{X: int(srcX), Y: int(srcY)}
	target := pathfinding.Point{X: int(req.destX), Y: int(req.destY)}
	path, err := mp.Pathfinder.FindPath(start, target)
	if err != nil {
		// ErrNoPath / OOB / unwalkable: rAthena ignores the move. Do not ack,
		// do not disconnect.
		return
	}
	// FindPath returns the path excluding start, including target, truncated to
	// MaxWalkPath. The kernel's single-frame move packets carry only src+dest
	// (the client interpolates the route), so the destination is the last cell
	// of the resolved path — or the start itself when the path is empty (a
	// click on the current cell, which rAthena treats as a no-op move that
	// still re-faces). A nil/empty path with no error means start==target.
	destX, destY := srcX, srcY
	if n := len(path); n > 0 {
		last := path[n-1]
		destX, destY = int16(last.X), int16(last.Y) //nolint:gosec // G115: int cell → int16 wire slot, map dims fit
	}

	moveStart := s.clock.MoveStart()

	// Self-ack: ZC_NOTIFY_PLAYERMOVE to the mover only.
	selfAck := packet.MapNotifyPlayerMoveResponse{
		MoveStartTime: moveStart,
		SrcX:          srcX,
		SrcY:          srcY,
		DestX:         destX,
		DestY:         destY,
	}
	if err := selfAck.Encode(connWriter{player.Conn}); err != nil {
		// The mover's own socket is dead — tear it down (idempotent) and stop;
		// there is no one to broadcast to and no point moving the AOI entity.
		s.dropPlayer(mp, player)
		return
	}

	// Observer broadcast: ZC_UNIT_WALKING to every other PC in AOI range of the
	// START cell (the neighbors that can see the move begin). A neighbor whose
	// write fails is dead — drop it so its stale entity stops polluting future
	// broadcasts. The mover is skipped (it got the self-ack, not the broadcast).
	walk := player.WalkUnit(srcX, srcY, destX, destY, moveStart)
	for _, e := range mp.AOI.QueryVisible(int(srcX), int(srcY)) {
		if e.ID == player.EntityID {
			continue
		}
		neighbor, ok := s.registry.ByAccount(uint32(e.ID))
		if !ok {
			continue // NPC/mob (M5+) or a torn-down player the grid hasn't removed
		}
		if err := walk.Encode(connWriter{neighbor.Conn}); err != nil {
			s.dropPlayer(mp, neighbor)
		}
	}

	// Commit the move: update the AOI entity's cell and the player's cached
	// position. AOI MoveEntity is atomic (cross-tower, ascending-lock-order);
	// player.SetPosition is locked so a concurrent SpawnUnit reads either the
	// old or the new cell, never a torn half-move. AOI edge transitions (a
	// move that takes the player out of a neighbor's sight, or brings it into
	// a new neighbor's sight) are an M4c+ refinement; the single-frame move
	// packet is sufficient for the combat-slice wire and the client tolerates
	// a stale AOI set until the next spawn/vanish event.
	if err := mp.AOI.MoveEntity(player.EntityID, int(destX), int(destY)); err != nil {
		// MoveEntity errors on OOB or unknown id; the pathfinder already
		// validated the target is in-bounds and walkable, so an error here is a
		// torn-down entity (a concurrent disconnect). Leave the cached position
		// as-is; the disconnect path owns teardown.
		return
	}
	player.SetPosition(destX, destY, playerFacing(srcX, srcY, destX, destY))
}

// facingDir is the 3x3 table mapping a move's (dy-sign, dx-sign) to an RO
// direction byte. Index order is [signIndex(dy)][signIndex(dx)] where the sign
// index is negative→0, zero→1, positive→2. RO packs directions clockwise:
// 0=N,1=NE,2=E,3=SE,4=S,5=SW,6=W,7=NW, with +y=south and +x=east, so:
//
//	dy<0 (north row): NW=7, N=0, NE=1
//	dy=0 (cardinal) :  W=6, -,  E=2
//	dy>0 (south row): SW=5, S=4, SE=3
//
// The center cell (no move) is unreachable: playerFacing returns early on a
// zero-distance move, so the table's [1][1] entry is never read.
var facingDir = [3][3]uint8{
	{7, 0, 1},
	{6, 0, 2},
	{5, 4, 3},
}

// signIndex maps a delta's sign to a table index: negative→0, zero→1,
// positive→2. It is the projection of a signed displacement onto the 3x3
// facingDir grid.
func signIndex(v int) int {
	if v < 0 {
		return 0
	}
	if v > 0 {
		return 2
	}
	return 1
}

// playerFacing returns the RO direction byte for a move from src to dest. RO
// directions are packed clockwise (0=N, 2=E, 4=S, 6=W, with diagonals
// between); a zero-distance move faces north (the caller retains the prior
// facing upstream, since SetPosition always needs a dir argument). rAthena's
// clif_parse_MoveTo faces the player toward the move target; the table lookup
// mirrors its map_dir octant selection without per-case branching.
func playerFacing(srcX, srcY, destX, destY int16) uint8 {
	dx, dy := int(destX)-int(srcX), int(destY)-int(srcY)
	if dx == 0 && dy == 0 {
		return 0
	}
	return facingDir[signIndex(dy)][signIndex(dx)]
}

// dropPlayer is the move-worker analogue of SpawnService.dropNeighbor: tear
// down a player whose Conn write failed, idempotently, so its stale AOI entity
// stops polluting future broadcasts. The player's own dispatch goroutine will
// observe the dead socket and close; this just stops the world from writing to
// it.
func (s *MoveService) dropPlayer(mp *domain.Map, p *domain.Player) {
	if p == nil {
		return
	}
	s.registry.Unregister(p.AccountID)
	_ = mp.AOI.RemoveEntity(p.EntityID)
}

// MoveHandler serves CZ_REQUEST_MOVE (0x0085) on the map-role dispatch table.
// It parses the target cell, resolves the player from the VERIFIED conn auth
// cache (never the packet — the packet carries no account id at all, and even
// if it did it would be client-controlled), and enqueues the move. A parse
// failure is returned so ProcessBytes logs it; an enqueue is always a nil
// error (a dropped move is not a session fault).
type MoveHandler struct {
	svc *MoveService
}

// NewMoveHandler builds a CZ_REQUEST_MOVE handler over the MoveService.
func NewMoveHandler(svc *MoveService) *MoveHandler {
	return &MoveHandler{svc: svc}
}

// Handle implements gateway/domain.PacketHandler for CZ_REQUEST_MOVE.
// accountID is sourced from conn.Auth().AccountID — the impersonation guard:
// the packet carries only a destination cell, so identity is the connection's
// verified auth, set by the CZ_ENTER gate.
func (h *MoveHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCZRequestMove(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CZ_REQUEST_MOVE: %w", err)
	}
	accountID := conn.Auth().AccountID
	if accountID == 0 {
		// No cached auth ⇒ the connection never passed the CZ_ENTER gate. A
		// move before enter is a protocol violation; return an error so
		// ProcessBytes logs it. The conn is not closed (handler errors are
		// tolerated by the gateway), but the move is dropped.
		return errors.New("move: connection has no verified account (CZ_ENTER not completed)")
	}
	return h.svc.RequestMove(ctx, conn, accountID, req)
}

// Compile-time check that MoveHandler satisfies the gateway handler shape.
var _ gwdomain.PacketHandler = (*MoveHandler)(nil).Handle
