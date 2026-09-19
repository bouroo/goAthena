// Package economy is the economy bounded-context module root.
package economy

import (
	"github.com/samber/do/v2"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	"github.com/bouroo/goAthena/internal/modules/economy/app"
	economydomain "github.com/bouroo/goAthena/internal/modules/economy/domain"
)

// Register provisions the EconomyService into the injector. It resolves the
// character repo and the zeny ledger from DI (economy writes zeny via the
// char table and appends to the ledger).
//
// The ledger is resolved lazily: if no GORM DB is wired (in-memory test path)
// the service still constructs, with ledger=nil, and calls that need the
// ledger fail with ErrLedgerAppendFailed. Production composition.go provides
// the GORMLedger before this provider runs.
func Register(inj do.Injector) {
	do.Provide(inj, func(i do.Injector) (*app.EconomyService, error) {
		repo := do.MustInvoke[chardomain.CharacterRepository](i)
		var ledger economydomain.LedgerRepository
		if l, err := do.Invoke[economydomain.LedgerRepository](i); err == nil {
			ledger = l
		}
		return app.NewEconomyService(repo, ledger), nil
	})
}
