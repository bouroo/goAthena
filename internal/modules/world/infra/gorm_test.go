//go:build integration

package infra_test

import (
	"context"
	"testing"

	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/internal/modules/world/infra"
)

// TestGORM_SkillTableRoundtrip proves the skill table schema and LoadSkills/SaveSkills
// roundtrip: a character with two learned skills can be saved and reloaded with the
// same skill list intact. This test is vet-only locally (no Docker); CI runs it.
func TestGORM_SkillTableRoundtrip(t *testing.T) {
	repo := infra.NewGORMWorldRepository(dbForTest(t))

	// Seed a char row in the char table (0-job = Novice).
	charID := uint32(1)
	ctx := context.Background()

	// SaveSkills: write two learned skills.
	skills := []domain.LearnedSkill{
		{SkillID: 5, Level: 3}, // SM_BASH level 3
		{SkillID: 8, Level: 1}, // NV_FIRSTAID level 1
	}
	if err := repo.SaveSkills(ctx, charID, skills); err != nil {
		t.Fatalf("SaveSkills: %v", err)
	}

	// LoadSkills: read back.
	loaded, err := repo.LoadSkills(ctx, charID)
	if err != nil {
		t.Fatalf("LoadSkills: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded %d skills, want 2", len(loaded))
	}
	got := map[int32]int16{}
	for _, s := range loaded {
		got[s.SkillID] = s.Level
	}
	if lvl, ok := got[5]; !ok || lvl != 3 {
		t.Errorf("skill 5: got level %d (ok=%v), want 3", lvl, ok)
	}
	if lvl, ok := got[8]; !ok || lvl != 1 {
		t.Errorf("skill 8: got level %d (ok=%v), want 1", lvl, ok)
	}

	// SaveSkills again with a different set (replace).
	newSkills := []domain.LearnedSkill{
		{SkillID: 5, Level: 5}, // SM_BASH upgraded
		{SkillID: 9, Level: 1}, // NV_BASIC replaced SM_BASH with NV_FIRSTAID
	}
	if err := repo.SaveSkills(ctx, charID, newSkills); err != nil {
		t.Fatalf("SaveSkills (replace): %v", err)
	}

	reloaded, err := repo.LoadSkills(ctx, charID)
	if err != nil {
		t.Fatalf("LoadSkills (reload): %v", err)
	}
	got = map[int32]int16{}
	for _, s := range reloaded {
		got[s.SkillID] = s.Level
	}
	if lvl, ok := got[5]; !ok || lvl != 5 {
		t.Errorf("skill 5 after upgrade: got level %d (ok=%v), want 5", lvl, ok)
	}
	if _, ok := got[8]; ok {
		t.Error("skill 8 should be gone after replace")
	}
	if lvl, ok := got[9]; !ok || lvl != 1 {
		t.Errorf("skill 9: got level %d (ok=%v), want 1", lvl, ok)
	}

	// SaveSkills with empty slice deletes all.
	if err := repo.SaveSkills(ctx, charID, nil); err != nil {
		t.Fatalf("SaveSkills (clear): %v", err)
	}
	cleared, err := repo.LoadSkills(ctx, charID)
	if err != nil {
		t.Fatalf("LoadSkills (after clear): %v", err)
	}
	if len(cleared) != 0 {
		t.Errorf("cleared %d skills, want 0", len(cleared))
	}
}
