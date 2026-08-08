//go:build unit

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bouroo/goAthena/internal/modules/character/app"
	"github.com/bouroo/goAthena/internal/modules/character/domain"
	"github.com/bouroo/goAthena/internal/modules/character/infra"
)

func newSvc(maxSlots int) (*app.CharService, *infra.MemorySessionStore) {
	repo := infra.NewMemoryCharacterRepository()
	sess := infra.NewMemorySessionStore()
	return app.NewCharService(repo, sess, maxSlots), sess
}

func TestAuthorize_ValidSession(t *testing.T) {
	svc, sess := newSvc(9)
	_ = sess.PutSession(context.Background(), domain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	})
	got, err := svc.Authorize(context.Background(), 2000001, 0x11111111)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if got.AccountID != 2000001 || got.Sex != 1 {
		t.Errorf("session = %+v", got)
	}
}

func TestAuthorize_NoSession(t *testing.T) {
	svc, _ := newSvc(9)
	_, err := svc.Authorize(context.Background(), 999, 0x11111111)
	if !errors.Is(err, domain.ErrSessionNotFound) {
		t.Errorf("err = %v, want ErrSessionNotFound", err)
	}
}

func TestAuthorize_BadLoginID1(t *testing.T) {
	svc, sess := newSvc(9)
	_ = sess.PutSession(context.Background(), domain.Session{
		AccountID: 2000001, LoginID1: 0xAAAA, Sex: 1,
	})
	_, err := svc.Authorize(context.Background(), 2000001, 0xBBBB)
	if !errors.Is(err, domain.ErrInvalidSession) {
		t.Errorf("err = %v, want ErrInvalidSession", err)
	}
}

func TestCreate_Success(t *testing.T) {
	svc, _ := newSvc(9)
	c, err := svc.Create(context.Background(), 2000001, 0, "Hero")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if c.Name != "Hero" || c.CharNum != 0 || c.BaseLevel != 1 {
		t.Errorf("created char = %+v", c)
	}
	if c.HP != c.MaxHP {
		t.Errorf("hp %d != maxhp %d", c.HP, c.MaxHP)
	}
}

func TestCreate_DuplicateName(t *testing.T) {
	svc, _ := newSvc(9)
	if _, err := svc.Create(context.Background(), 2000001, 0, "Hero"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Create(context.Background(), 2000001, 1, "Hero")
	if !errors.Is(err, domain.ErrNameTaken) {
		t.Errorf("err = %v, want ErrNameTaken", err)
	}
}

func TestCreate_DuplicateSlot(t *testing.T) {
	svc, _ := newSvc(9)
	if _, err := svc.Create(context.Background(), 2000001, 0, "Hero"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Create(context.Background(), 2000001, 0, "Other")
	if !errors.Is(err, domain.ErrSlotTaken) {
		t.Errorf("err = %v, want ErrSlotTaken", err)
	}
}

func TestCreate_SlotLimitExceeded(t *testing.T) {
	svc, _ := newSvc(1)
	if _, err := svc.Create(context.Background(), 2000001, 0, "A"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Create(context.Background(), 2000001, 1, "B")
	if !errors.Is(err, domain.ErrSlotTaken) {
		t.Errorf("err = %v, want ErrSlotTaken", err)
	}
}

func TestList_ReturnsAccountChars(t *testing.T) {
	svc, _ := newSvc(9)
	_, _ = svc.Create(context.Background(), 2000001, 2, "C")
	_, _ = svc.Create(context.Background(), 2000001, 0, "A")
	chars, err := svc.List(context.Background(), 2000001)
	if err != nil {
		t.Fatal(err)
	}
	if len(chars) != 2 {
		t.Fatalf("len = %d, want 2", len(chars))
	}
}

func TestDelete_Success(t *testing.T) {
	svc, _ := newSvc(9)
	c, _ := svc.Create(context.Background(), 2000001, 0, "Hero")
	if err := svc.Delete(context.Background(), c.ID, 2000001); err != nil {
		t.Fatalf("delete: %v", err)
	}
	chars, _ := svc.List(context.Background(), 2000001)
	if len(chars) != 0 {
		t.Errorf("chars after delete = %d, want 0", len(chars))
	}
}

func TestDelete_NotFound(t *testing.T) {
	svc, _ := newSvc(9)
	err := svc.Delete(context.Background(), 99999, 2000001)
	if !errors.Is(err, domain.ErrCharacterNotFound) {
		t.Errorf("err = %v, want ErrCharacterNotFound", err)
	}
}
