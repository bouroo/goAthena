//go:build unit

package app_test

import (
	"context"
	"errors"
	"testing"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	"github.com/bouroo/goAthena/internal/modules/economy/app"
)

// fakeCharRepo is an in-memory chardomain.CharacterRepository for economy tests.
type fakeCharRepo struct {
	chars map[chardomain.CharID]chardomain.Character
}

func (f *fakeCharRepo) ListByAccount(_ context.Context, accountID uint32) ([]chardomain.Character, error) {
	var out []chardomain.Character
	for _, c := range f.chars {
		if c.AccountID == accountID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeCharRepo) Create(_ context.Context, c chardomain.Character) (chardomain.Character, error) {
	f.chars[c.ID] = c
	return c, nil
}

func (f *fakeCharRepo) Delete(_ context.Context, id chardomain.CharID, _ uint32) error {
	delete(f.chars, id)
	return nil
}

func (f *fakeCharRepo) NameExists(_ context.Context, name string) (bool, error) {
	for _, c := range f.chars {
		if c.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeCharRepo) UpdateZeny(_ context.Context, id chardomain.CharID, zeny uint32) error {
	c, ok := f.chars[id]
	if !ok {
		return chardomain.ErrCharacterNotFound
	}
	c.Zeny = zeny
	f.chars[id] = c
	return nil
}

func (f *fakeCharRepo) SetDeleteDate(_ context.Context, id chardomain.CharID, _ uint32, _ uint32) error {
	return nil
}

func (f *fakeCharRepo) FindByID(_ context.Context, id chardomain.CharID) (chardomain.Character, error) {
	c, ok := f.chars[id]
	if !ok {
		return chardomain.Character{}, chardomain.ErrCharacterNotFound
	}
	return c, nil
}

func TestEconomyGetZeny(t *testing.T) {
	repo := &fakeCharRepo{chars: map[chardomain.CharID]chardomain.Character{
		1: {ID: 1, Zeny: 123456},
	}}
	svc := app.NewEconomyService(repo)

	got, err := svc.GetZeny(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetZeny: %v", err)
	}
	if got != 123456 {
		t.Errorf("GetZeny = %d, want 123456", got)
	}

	if _, err := svc.GetZeny(context.Background(), 2); !errors.Is(err, chardomain.ErrCharacterNotFound) {
		t.Errorf("missing char err = %v, want ErrCharacterNotFound", err)
	}
}

func TestEconomyDeductCreditGetZenyRoundTrip(t *testing.T) {
	repo := &fakeCharRepo{chars: map[chardomain.CharID]chardomain.Character{
		7: {ID: 7, Zeny: 1000},
	}}
	svc := app.NewEconomyService(repo)
	ctx := context.Background()

	if err := svc.DeductZeny(ctx, 7, 250); err != nil {
		t.Fatalf("deduct: %v", err)
	}
	if got, _ := svc.GetZeny(ctx, 7); got != 750 {
		t.Errorf("after deduct = %d, want 750", got)
	}
	if err := svc.CreditZeny(ctx, 7, 50); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if got, _ := svc.GetZeny(ctx, 7); got != 800 {
		t.Errorf("after credit = %d, want 800", got)
	}
	// Overdraft is rejected and leaves the balance intact.
	if err := svc.DeductZeny(ctx, 7, 999999); err == nil {
		t.Errorf("overdraft deduct: want error, got nil")
	}
	if got, _ := svc.GetZeny(ctx, 7); got != 800 {
		t.Errorf("after rejected overdraft = %d, want 800", got)
	}
}
