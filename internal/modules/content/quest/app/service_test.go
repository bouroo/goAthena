//go:build unit

package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bouroo/goAthena/internal/modules/content/quest/app"
	"github.com/bouroo/goAthena/internal/modules/content/quest/domain"
	"github.com/bouroo/goAthena/internal/modules/content/quest/infra"
)

func newSvc() *app.QuestService {
	return app.NewQuestService(infra.NewMemoryQuestRepository())
}

func TestGetVar_UnsetIsZero(t *testing.T) {
	svc := newSvc()
	v, err := svc.GetVar(context.Background(), 150001, "Healer", "KillCount")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if v != 0 {
		t.Errorf("unset = %d, want 0", v)
	}
}

func TestSetThenGetVar(t *testing.T) {
	svc := newSvc()
	if err := svc.SetVar(context.Background(), 150001, "Healer", "KillCount", 7); err != nil {
		t.Fatalf("set: %v", err)
	}
	v, err := svc.GetVar(context.Background(), 150001, "Healer", "KillCount")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if v != 7 {
		t.Errorf("get = %d, want 7", v)
	}
}

func TestSetVar_UpsertsOverExisting(t *testing.T) {
	svc := newSvc()
	if err := svc.SetVar(context.Background(), 150001, "Quest", "Step", 1); err != nil {
		t.Fatalf("set 1: %v", err)
	}
	if err := svc.SetVar(context.Background(), 150001, "Quest", "Step", 5); err != nil {
		t.Fatalf("set 2: %v", err)
	}
	v, _ := svc.GetVar(context.Background(), 150001, "Quest", "Step")
	if v != 5 {
		t.Errorf("get = %d, want 5 (upsert)", v)
	}
}

func TestSetVar_Negative(t *testing.T) {
	svc := newSvc()
	// Negative quest state is valid (rAthena permits it; some scripts use it
	// to flag invalid paths).
	if err := svc.SetVar(context.Background(), 150001, "Q", "BadStep", -1); err != nil {
		t.Fatalf("set: %v", err)
	}
	v, _ := svc.GetVar(context.Background(), 150001, "Q", "BadStep")
	if v != -1 {
		t.Errorf("get = %d, want -1", v)
	}
}

func TestGetVarOfNPC(t *testing.T) {
	svc := newSvc()
	if err := svc.SetVar(context.Background(), 150001, "OtherNPC", "Flag", 42); err != nil {
		t.Fatalf("set: %v", err)
	}
	v, err := svc.GetVarOfNPC(context.Background(), 150001, "OtherNPC", "Flag")
	if err != nil {
		t.Fatalf("getofnpc: %v", err)
	}
	if v != 42 {
		t.Errorf("getofnpc = %d, want 42", v)
	}
}

func TestSetVar_RejectsOverlongName(t *testing.T) {
	svc := newSvc()
	longName := strings.Repeat("a", 33) // MaxVarName = 32
	err := svc.SetVar(context.Background(), 150001, "NPC", longName, 1)
	if !errors.Is(err, domain.ErrQuestInvalidName) {
		t.Errorf("expected ErrQuestInvalidName, got %v", err)
	}
}

func TestGetVar_RejectsOverlongName(t *testing.T) {
	svc := newSvc()
	longName := strings.Repeat("a", 25) // MaxNpcName = 24
	_, err := svc.GetVar(context.Background(), 150001, longName, "x")
	if !errors.Is(err, domain.ErrQuestInvalidName) {
		t.Errorf("expected ErrQuestInvalidName, got %v", err)
	}
}

func TestSetVar_RejectsEmptyNames(t *testing.T) {
	svc := newSvc()
	if err := svc.SetVar(context.Background(), 150001, "", "x", 1); !errors.Is(err, domain.ErrQuestInvalidName) {
		t.Errorf("empty npc_name: expected ErrQuestInvalidName, got %v", err)
	}
	if err := svc.SetVar(context.Background(), 150001, "NPC", "", 1); !errors.Is(err, domain.ErrQuestInvalidName) {
		t.Errorf("empty var_name: expected ErrQuestInvalidName, got %v", err)
	}
}

func TestListByChar(t *testing.T) {
	svc := newSvc()
	if err := svc.SetVar(context.Background(), 150001, "A", "x", 1); err != nil {
		t.Fatalf("set A/x: %v", err)
	}
	if err := svc.SetVar(context.Background(), 150001, "B", "y", 2); err != nil {
		t.Fatalf("set B/y: %v", err)
	}
	if err := svc.SetVar(context.Background(), 150002, "A", "x", 99); err != nil {
		t.Fatalf("set 150002: %v", err)
	}
	rows, err := svc.ListByChar(context.Background(), 150001)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("rows = %d, want 2", len(rows))
	}
}

func TestGetVar_ParseFailureFallsBackToZero(t *testing.T) {
	// Drive Set with text through the repo directly: the service parses
	// decimal; a non-numeric stored value reads as 0 (matches rAthena
	// tolerance for hand-edited quest rows).
	repo := infra.NewMemoryQuestRepository()
	if err := repo.Set(context.Background(), 150001, "NPC", "Var", "not-a-number"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	svc := app.NewQuestService(repo)
	v, err := svc.GetVar(context.Background(), 150001, "NPC", "Var")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if v != 0 {
		t.Errorf("garbage = %d, want 0", v)
	}
}
