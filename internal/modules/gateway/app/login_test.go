//go:build unit

package app

import (
	"errors"
	"testing"

	"github.com/bouroo/goAthena/internal/modules/account/domain"
)

func TestRefuseCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want uint32
	}{
		{"not found", domain.ErrAccountNotFound, refuseUnregistered},
		{"bad password", domain.ErrInvalidPassword, refuseBadPassword},
		{"banned", domain.ErrAccountBanned, refuseProhibited},
		{"unknown", errors.New("boom"), refuseProhibited},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := refuseCode(tc.err); got != tc.want {
				t.Errorf("refuseCode(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

func TestSexByte(t *testing.T) {
	cases := []struct {
		sex  domain.Sex
		want uint8
	}{
		{domain.SexFemale, 0},
		{domain.SexMale, 1},
		{domain.SexServer, 2},
	}
	for _, tc := range cases {
		if got := sexByte(tc.sex); got != tc.want {
			t.Errorf("sexByte(%q) = %d, want %d", tc.sex, got, tc.want)
		}
	}
}

func TestIPToWire(t *testing.T) {
	got, err := ipToWire("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if got == 0 {
		t.Error("ipToWire(127.0.0.1) = 0, want non-zero")
	}
	if _, err := ipToWire("not-an-ip.invalid"); err == nil {
		t.Error("expected error for bad host")
	}
}
