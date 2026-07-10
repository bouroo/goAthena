package service

import (
	"context"
	"fmt"

	"github.com/bouroo/goAthena/internal/features/identity/domain"
	statsdomain "github.com/bouroo/goAthena/internal/features/stats/domain"
)

// ApplyLevelUp applies a base-level-up computed by the gateway. The
// fromLevel is revalidated inside the repository via an optimistic-lock
// WHERE clause — when a concurrent level-up already advanced the char,
// rows==0 and applied=false is returned so the caller can re-read and
// skip.
func (s *identityService) ApplyLevelUp(
	ctx context.Context,
	accountID, charID, fromLevel, toLevel, grantedStatusPoints uint32,
) (newLevel, newStatusPoint uint32, applied bool, err error) {
	if accountID == 0 || charID == 0 {
		return 0, 0, false, fmt.Errorf("apply level up (account=%d, char=%d): %w", accountID, charID, domain.ErrCharacterNotFound)
	}
	return s.characters.ApplyLevelUp(ctx, accountID, charID, fromLevel, toLevel, grantedStatusPoints)
}

// AllocateStat raises one base stat by amount, computing the cost from
// stats/domain.StatCost and deducting it from status_point. Returns a
// StatResult code (1=OK, 2=INSUFFICIENT, 3=MAX, 4=INVALID) rather than
// an error: stat allocation failures are protocol-level outcomes so the
// gateway can carry them inside the ZC_STATUS_CHANGE ack.
func (s *identityService) AllocateStat(
	ctx context.Context,
	accountID, charID, statID, amount uint32,
) (result int, newValue, newStatusPoint uint32, err error) {
	if accountID == 0 || charID == 0 {
		return 0, 0, 0, fmt.Errorf("allocate stat (account=%d, char=%d): %w", accountID, charID, domain.ErrCharacterNotFound)
	}

	statType := statsdomain.StatType(statID)
	if !statType.Valid() {
		return 4, 0, 0, nil
	}
	if amount == 0 {
		return 4, 0, 0, nil
	}

	char, err := s.characters.GetByID(ctx, accountID, charID)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("allocate stat load char: %w", err)
	}

	currentVal := statValueFromSummary(char, statType)
	cost := statsdomain.StatCost(currentVal, int(amount))
	column := statTypeToColumn(statType)

	newVal, newSp, repoResult, err := s.characters.AllocateStat(ctx, accountID, charID, column, uint8(amount), cost)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("allocate stat: %w", err)
	}
	switch repoResult {
	case 0:
		return 1, newVal, newSp, nil
	case 1:
		return 2, newVal, newSp, nil
	case 2:
		return 3, newVal, newSp, nil
	default:
		return 4, 0, 0, nil
	}
}

// statTypeToColumn maps a StatType onto the character table column name.
// The column names match the Go struct field names on CharModel and are
// used by conditionally-UPDATEd column targets in AllocateStat.
func statTypeToColumn(t statsdomain.StatType) string {
	switch t {
	case statsdomain.StatStr:
		return "str"
	case statsdomain.StatAgi:
		return "agi"
	case statsdomain.StatVit:
		return "vit"
	case statsdomain.StatInt:
		return "int"
	case statsdomain.StatDex:
		return "dex"
	case statsdomain.StatLuk:
		return "luk"
	}
	return ""
}

// statValueFromSummary extracts the uint8 value of statType from a
// CharacterSummary. The uint16→uint8 convert is safe here — stats cap
// at MaxStat (99) which fits in uint8.
func statValueFromSummary(c *domain.CharacterSummary, t statsdomain.StatType) uint8 {
	switch t {
	case statsdomain.StatStr:
		return uint8(c.Str)
	case statsdomain.StatAgi:
		return uint8(c.Agi)
	case statsdomain.StatVit:
		return uint8(c.Vit)
	case statsdomain.StatInt:
		return uint8(c.Int)
	case statsdomain.StatDex:
		return uint8(c.Dex)
	case statsdomain.StatLuk:
		return uint8(c.Luk)
	}
	return 0 //nolint:gosec // stats cap at 99
}
