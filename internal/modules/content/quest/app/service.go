// Package app implements the quest bounded context use cases: read and write
// persistent NPC variables for a character. QuestVars are the storage behind
// rAthena's `set npc_var, value` and `getvariableofnpc` script builtins; the
// engine writes them on every NPC-script `set` and reads them on every
// subsequent script invocation that mentions the variable.
package app

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/bouroo/goAthena/internal/modules/content/quest/domain"
)

// QuestService is the quest use-case service. It enforces the name-length
// bounds from the migration VARCHAR limits and converts integer quest state
// to/from text on the boundary (the storage layer is text-only).
type QuestService struct {
	repos domain.QuestRepository
}

// NewQuestService builds a QuestService backed by repo.
func NewQuestService(repo domain.QuestRepository) *QuestService {
	return &QuestService{repos: repo}
}

// MaxNpcName / MaxVarName mirror the migration VARCHAR lengths. A longer
// name would silently truncate at INSERT time, so the service refuses them.
const (
	MaxNpcName = 24
	MaxVarName = 32
)

// GetVar returns the integer value of (charID, npcName, varName). An unset
// variable returns 0 — matches rAthena's "unset integer reads as 0"
// (script.cpp get_val). A parse failure on a stored string falls back to 0
// too: the rAthena quest table is text, so a hand-edited row with garbage
// in `value` would otherwise crash a script on read.
func (s *QuestService) GetVar(ctx context.Context, charID uint32, npcName, varName string) (int64, error) {
	if err := validateNames(npcName, varName); err != nil {
		return 0, err
	}
	row, ok, err := s.repos.Get(ctx, charID, npcName, varName)
	if err != nil {
		return 0, fmt.Errorf("quest get: %w", err)
	}
	if !ok {
		return 0, nil
	}
	v, err := strconv.ParseInt(row.Value, 10, 64)
	if err != nil {
		return 0, nil // text that's not an int reads as 0
	}
	return v, nil
}

// SetVar stores the integer value of (charID, npcName, varName). The value
// is decimal-encoded to text — matching rAthena's quest table shape so an
// existing rAthena dump loads without a transform.
func (s *QuestService) SetVar(ctx context.Context, charID uint32, npcName, varName string, value int64) error {
	if err := validateNames(npcName, varName); err != nil {
		return err
	}
	if err := s.repos.Set(ctx, charID, npcName, varName, strconv.FormatInt(value, 10)); err != nil {
		return fmt.Errorf("quest set: %w", err)
	}
	return nil
}

// GetVarOfNPC returns the integer value of (charID, npcName, varName) where
// npcName is supplied by the caller (`getvariableofnpc` argument). Same
// semantics as GetVar; the npcName scoping is the only difference.
func (s *QuestService) GetVarOfNPC(ctx context.Context, charID uint32, npcName, varName string) (int64, error) {
	return s.GetVar(ctx, charID, npcName, varName)
}

// ListByChar returns every quest row owned by charID. Useful for admin
// tools / debug dashboards; not on the hot script path.
func (s *QuestService) ListByChar(ctx context.Context, charID uint32) ([]domain.QuestVar, error) {
	rows, err := s.repos.ListByChar(ctx, charID)
	if err != nil {
		return nil, fmt.Errorf("quest list: %w", err)
	}
	return rows, nil
}

// validateNames rejects names that would silently truncate at INSERT time.
// The migration VARCHAR lengths are the source of truth.
func validateNames(npcName, varName string) error {
	if len(npcName) == 0 || len(npcName) > MaxNpcName {
		return fmt.Errorf("%w: npc_name len=%d not in (0, %d]", domain.ErrQuestInvalidName, len(npcName), MaxNpcName)
	}
	if len(varName) == 0 || len(varName) > MaxVarName {
		return fmt.Errorf("%w: var_name len=%d not in (0, %d]", domain.ErrQuestInvalidName, len(varName), MaxVarName)
	}
	return nil
}

// Compile-time guarantee the error sentinel exists (avoids the unused-import
// lint when the service grows new error variants in future revisions).
var _ = errors.New
