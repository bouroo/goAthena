//go:build unit

package domain_test

import (
	"testing"

	"github.com/bouroo/goAthena/internal/modules/economy/domain"
)

func TestZeny_Deduct(t *testing.T) {
	z, _ := domain.NewZeny(1000)
	got, err := z.Deduct(300)
	if err != nil {
		t.Fatal(err)
	}
	if got.Amount() != 700 {
		t.Errorf("amount = %d, want 700", got.Amount())
	}
}

func TestZeny_DeductInsufficient(t *testing.T) {
	z, _ := domain.NewZeny(100)
	_, err := z.Deduct(200)
	if err != domain.ErrInsufficientFunds {
		t.Errorf("err = %v, want ErrInsufficientFunds", err)
	}
}

func TestZeny_Credit(t *testing.T) {
	z, _ := domain.NewZeny(500)
	got, err := z.Credit(200)
	if err != nil {
		t.Fatal(err)
	}
	if got.Amount() != 700 {
		t.Errorf("amount = %d, want 700", got.Amount())
	}
}

func TestZeny_CreditOverflow(t *testing.T) {
	z, _ := domain.NewZeny(2000000000) // near int32 max
	_, err := z.Credit(2000000000)
	if err != domain.ErrOverflow {
		t.Errorf("err = %v, want ErrOverflow", err)
	}
}

func TestZeny_CanAfford(t *testing.T) {
	z, _ := domain.NewZeny(500)
	if !z.CanAfford(400) {
		t.Error("should afford 400")
	}
	if z.CanAfford(600) {
		t.Error("should not afford 600")
	}
}
