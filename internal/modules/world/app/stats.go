package app

import (
	"context"
	"errors"
	"fmt"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// Sentinel errors for the stat-change use case. The handler checks these with
// errors.Is so wrapping is preserved across fmt.Errorf("...: %w", err).
var (
	errInsufficientPoints = errors.New("stats: insufficient status points")
	errInvalidStatID      = errors.New("stats: invalid stat ID")
)

// statCost returns the status-point cost to raise a stat from its current value.
// Matches rAthena's pre-renewal formula: [(current - 1) / 10] + 2.
func statCost(current uint16) int {
	return int((current-1)/10) + 2
}

// StatsService handles the CZ_STATUS_CHANGE use case: a player spends earned
// status points to raise one base stat. It validates the stat ID, checks that
// the character has enough points, deducts the cost, increments the stat, persists
// through SaveProgression, and notifies the client.
type StatsService struct {
	chars    chardomain.CharacterRepository
	registry *domain.PlayerRegistry
}

// NewStatsService binds the character repository and live player registry.
func NewStatsService(chars chardomain.CharacterRepository, registry *domain.PlayerRegistry) *StatsService {
	return &StatsService{chars: chars, registry: registry}
}

// IncreaseStat processes a stat-point allocation for one character. It validates
// the stat ID, deducts the cost, increments the stat, persists, and returns the
// updated stat value and remaining status points. All other fields of the
// Progression write are copied unchanged from the loaded character.
func (s *StatsService) IncreaseStat(ctx context.Context, accountID uint32, charID uint32, statID uint16) (newVal uint16, remainingPoints uint32, _ error) {
	if statID < 13 || statID > 18 {
		return 0, 0, errInvalidStatID
	}

	c, err := s.chars.GetByID(ctx, accountID, charID)
	if err != nil {
		return 0, 0, fmt.Errorf("stats: load character: %w", err)
	}

	current := statValue(c, statID)
	cost := statCost(current)

	if c.StatusPoint < uint32(cost) { //nolint:gosec // G115: cost is bounded by statCost formula (max ~12)
		return current, c.StatusPoint, errInsufficientPoints
	}

	c.StatusPoint -= uint32(cost) //nolint:gosec // G115: same bound
	setStatValue(c, statID, current+1)

	if err := s.chars.SaveProgression(ctx, accountID, charID, chardomain.ProgressionOf(c)); err != nil {
		return 0, 0, fmt.Errorf("stats: persist progression: %w", err)
	}

	return current + 1, c.StatusPoint, nil
}

// statValue returns the current value of the stat identified by the rAthena
// SP_* parameter ID.
func statValue(c *chardomain.Character, statID uint16) uint16 {
	switch statID {
	case 13: // SP_STR
		return c.Str
	case 14: // SP_AGI
		return c.Agi
	case 15: // SP_VIT
		return c.Vit
	case 16: // SP_INT
		return c.Int
	case 17: // SP_DEX
		return c.Dex
	case 18: // SP_LUK
		return c.Luk
	default:
		return 0
	}
}

// setStatValue mutates the character in-place to increment the stat identified
// by the rAthena SP_* parameter ID.
func setStatValue(c *chardomain.Character, statID uint16, newVal uint16) {
	switch statID {
	case 13:
		c.Str = newVal
	case 14:
		c.Agi = newVal
	case 15:
		c.Vit = newVal
	case 16:
		c.Int = newVal
	case 17:
		c.Dex = newVal
	case 18:
		c.Luk = newVal
	}
}

// StatsHandler serves CZ_STATUS_CHANGE (0x00bb) on the map-role dispatch table.
type StatsHandler struct {
	svc *StatsService
}

// NewStatsHandler builds a CZ_STATUS_CHANGE handler over the StatsService.
func NewStatsHandler(chars chardomain.CharacterRepository, registry *domain.PlayerRegistry) *StatsHandler {
	return &StatsHandler{svc: NewStatsService(chars, registry)}
}

// Handle implements gateway/domain.PacketHandler for CZ_STATUS_CHANGE. It
// resolves the player from the live registry, applies the stat allocation, and
// sends ZC_STATUS_CHANGE_ACK plus ZC_PAR_CHANGE frames.
func (h *StatsHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCZStatusChange(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CZ_STATUS_CHANGE: %w", err)
	}
	accountID := conn.Auth().AccountID
	if accountID == 0 {
		return nil
	}

	player, ok := h.svc.registry.ByAccount(accountID)
	if !ok {
		return nil
	}

	newVal, remaining, err := h.svc.IncreaseStat(ctx, accountID, player.CharID, req.StatusID)
	if err != nil {
		if errors.Is(err, errInsufficientPoints) {
			ack := packet.ZCStatusChangeAck{StatusID: req.StatusID, Result: 1, Value: 0}
			_ = ack.Encode(connWriter{conn})
			return nil
		}
		if errors.Is(err, errInvalidStatID) {
			// Silently drop forged stat IDs — the client UI never offers them.
			return nil
		}
		return fmt.Errorf("stats: increase stat: %w", err)
	}

	// ZC_STATUS_CHANGE_ACK: 6 bytes
	if err := (packet.ZCStatusChangeAck{StatusID: req.StatusID, Result: 0, Value: uint8(newVal)}).Encode(connWriter{conn}); err != nil { //nolint:gosec // G115: stat values are 1-99, fit uint8
		return fmt.Errorf("stats: encode ZC_STATUS_CHANGE_ACK: %w", err)
	}

	// ZC_PAR_CHANGE for the raised stat and the updated StatusPoint pool
	for _, change := range []packet.ParChangeResponse{
		{VarID: req.StatusID, Count: int32(newVal)},            //nolint:gosec // G115: stat values fit int32
		{VarID: packet.SPStatusPoint, Count: int32(remaining)}, //nolint:gosec // G115: status points fit int32
	} {
		if err := change.Encode(connWriter{conn}); err != nil {
			return fmt.Errorf("stats: encode ZC_PAR_CHANGE var %d: %w", change.VarID, err)
		}
	}
	return nil
}

// Compile-time check that StatsHandler satisfies the gateway handler shape.
var _ gwdomain.PacketHandler = (*StatsHandler)(nil).Handle
