//go:build unit

package domain_test

import (
	"testing"
	"time"

	"github.com/bouroo/goAthena/internal/modules/economy/domain"
)

func TestZenyTransactionValid(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		tx   domain.ZenyTransaction
		want bool
	}{
		{
			name: "credit ok",
			tx:   domain.ZenyTransaction{CharID: 1, Amount: 100, Reason: domain.ReasonMobKill, CreatedAt: now},
			want: true,
		},
		{
			name: "debit ok",
			tx:   domain.ZenyTransaction{CharID: 1, Amount: -50, Reason: domain.ReasonShopBuy, CreatedAt: now},
			want: true,
		},
		{
			name: "missing char id",
			tx:   domain.ZenyTransaction{Amount: 100, Reason: domain.ReasonMobKill, CreatedAt: now},
			want: false,
		},
		{
			name: "zero amount",
			tx:   domain.ZenyTransaction{CharID: 1, Amount: 0, Reason: domain.ReasonMobKill, CreatedAt: now},
			want: false,
		},
		{
			name: "missing reason",
			tx:   domain.ZenyTransaction{CharID: 1, Amount: 100, CreatedAt: now},
			want: false,
		},
		{
			name: "missing timestamp",
			tx:   domain.ZenyTransaction{CharID: 1, Amount: 100, Reason: domain.ReasonMobKill},
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.tx.Valid(); got != c.want {
				t.Errorf("Valid() = %v, want %v", got, c.want)
			}
		})
	}
}
