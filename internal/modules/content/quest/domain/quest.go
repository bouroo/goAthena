// Package domain holds the quest bounded context's pure domain model: the
// QuestVar aggregate (mirrors the rAthena `quest` table) and the repository
// port. QuestVars are NPC-script-managed named values per character: kill
// counters, reward flags, dialogue branches, and the like.
package domain

import (
	"context"
	"errors"
)

// QuestVar is one row in the rAthena `quest` table — a persistent named
// value scoped to (charID, npcName, varName). The triple is the primary
// key; an unset variable is simply absent (the storage layer returns zero
// on lookup, matching rAthena's "unset integer reads as 0").
type QuestVar struct {
	CharID  uint32 `gorm:"column:char_id;primaryKey"`
	NpcName string `gorm:"column:npc_name;primaryKey;size:24"`
	VarName string `gorm:"column:var_name;primaryKey;size:32"`
	Value   string `gorm:"column:value;size:255"`
}

// TableName fixes the legacy table name so GORM does not pluralize it.
func (QuestVar) TableName() string { return "quest" }

// Quest errors.
var (
	// ErrQuestInvalidName is returned when npcName or varName fails the
	// rAthena schema bound (24 / 32 bytes). The bounds are conservative —
	// the migration VARCHAR lengths match them — so a longer name would
	// silently truncate at INSERT time otherwise.
	ErrQuestInvalidName = errors.New("quest: invalid npc/var name length")
)

// QuestRepository is the persistence port for the quest aggregate.
//
// Implementations are expected to upsert (last-writer-wins), matching
// rAthena's quest table semantics. The engine has no version column;
// the script author owns the write path.
type QuestRepository interface {
	// Get returns the row for (charID, npcName, varName) or false.
	Get(ctx context.Context, charID uint32, npcName, varName string) (QuestVar, bool, error)
	// Set upserts the row, replacing the existing value.
	Set(ctx context.Context, charID uint32, npcName, varName, value string) error
	// ListByChar returns every quest row owned by charID. Useful for
	// admin tools / debug dashboards; not on the hot script path.
	ListByChar(ctx context.Context, charID uint32) ([]QuestVar, error)
}
