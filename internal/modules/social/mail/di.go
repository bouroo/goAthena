// Package mail is the mail bounded-context module root.
//
// Mail (RODEX) moves zeny and items between characters. Zeny movements are
// tagged with ReasonMailSend / ReasonMailCollect so the zeny ledger carries
// the audit trail (the shop's economyAdapter pattern: the adapter stamps the
// reason, the mail module never names ledger reasons itself).
package mail

import (
	"context"
	"fmt"

	"github.com/samber/do/v2"
	"gorm.io/gorm"

	economyapp "github.com/bouroo/goAthena/internal/modules/economy/app"
	economydomain "github.com/bouroo/goAthena/internal/modules/economy/domain"
	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	"github.com/bouroo/goAthena/internal/modules/social/mail/app"
	"github.com/bouroo/goAthena/internal/modules/social/mail/domain"
	"github.com/bouroo/goAthena/internal/modules/social/mail/infra"
)

// economyAdapter bridges economy.EconomyService → the mail app's
// EconomyPort, tagging each movement with the mail reason so the zeny ledger
// shows "mail_send 1000 from A to B" rather than an unknown debit.
type economyAdapter struct {
	get    func(context.Context, uint32) (int32, error)
	deduct func(context.Context, uint32, int32) error
	credit func(context.Context, uint32, int32) error
}

func (a economyAdapter) GetZeny(ctx context.Context, charID uint32) (int32, error) {
	return a.get(ctx, charID)
}

func (a economyAdapter) DeductZeny(ctx context.Context, charID uint32, amount int32) error {
	return a.deduct(ctx, charID, amount)
}

func (a economyAdapter) CreditZeny(ctx context.Context, charID uint32, amount int32) error {
	return a.credit(ctx, charID, amount)
}

// Register provisions the GORM mail repo and the MailService into the
// injector. The GORM handle is resolved lazily so a down database surfaces as
// a resolution error at use time rather than at boot.
func Register(inj do.Injector) {
	do.Provide(inj, func(i do.Injector) (*infra.GORMMailRepository, error) {
		gdb := do.MustInvoke[*gorm.DB](i)
		return infra.NewGORMMailRepository(gdb), nil
	})
	do.Provide(inj, func(i do.Injector) (domain.MailRepository, error) {
		return do.MustInvoke[*infra.GORMMailRepository](i), nil
	})
	do.Provide(inj, func(i do.Injector) (*app.MailService, error) {
		repo := do.MustInvoke[*infra.GORMMailRepository](i)
		itemRepo := do.MustInvoke[invdomain.ItemRepository](i)
		econSvc := do.MustInvoke[*economyapp.EconomyService](i)
		inv := invPort{repo: itemRepo}
		deduct := func(ctx context.Context, charID uint32, amount int32) error {
			return econSvc.DeductZenyFor(ctx, charID, amount, economyapp.LedgerEntry{Reason: economydomain.ReasonMailSend})
		}
		credit := func(ctx context.Context, charID uint32, amount int32) error {
			return econSvc.CreditZenyFor(ctx, charID, amount, economyapp.LedgerEntry{Reason: economydomain.ReasonMailCollect})
		}
		econ := economyAdapter{deduct: deduct, credit: credit}
		econ.get = func(ctx context.Context, charID uint32) (int32, error) {
			return econSvc.GetZeny(ctx, charID)
		}
		return app.NewMailService(repo, econ, inv), nil
	})
}

// invPort adapts the inventory ItemRepository to the mail app's InventoryPort
// (Add/Remove). The mail module stays off inventory/app for the same reason
// trade does: a narrow local port keeps the dependency on the domain type.
type invPort struct {
	repo invdomain.ItemRepository
}

func (p invPort) Add(ctx context.Context, charID, nameID uint32, amount int) (invdomain.Item, error) {
	it, err := p.repo.Add(ctx, charID, nameID, amount)
	if err != nil {
		return invdomain.Item{}, fmt.Errorf("mail inventory add: %w", err)
	}
	return it, nil
}

func (p invPort) Remove(ctx context.Context, id invdomain.ItemID, amount int) error {
	if err := p.repo.Remove(ctx, id, amount); err != nil {
		return fmt.Errorf("mail inventory remove: %w", err)
	}
	return nil
}
