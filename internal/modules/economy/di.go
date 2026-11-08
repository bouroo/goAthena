// Package economy is the economy bounded-context module root.
package economy

import (
	"log/slog"

	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/config"
	natsinfra "github.com/bouroo/goAthena/internal/infrastructure/messaging/nats"
	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	"github.com/bouroo/goAthena/internal/modules/economy/app"
	economydomain "github.com/bouroo/goAthena/internal/modules/economy/domain"
	"github.com/bouroo/goAthena/internal/modules/economy/remote"
)

// Register provisions the economy Service into the injector and returns a
// closer for the resources it opened (nil when nothing to close).
//
// Transport switch (config.NATSConfig.Economy):
//   - "local" (default): the in-process *app.EconomyService, resolving the
//     character repo and zeny ledger from DI. The ledger resolves lazily — no
//     GORM DB (in-memory test path) leaves it nil and movements fail with
//     ErrLedgerAppendFailed.
//   - "remote": a NATS request/reply Proxy against a `goathena serve-economy`
//     host. The dial does not block on the broker (retry-on-failed-connect),
//     so a zone may boot before the bus; calls fail individually until the
//     route exists. A malformed URL aborts registration — economy will not
//     resolve and commerce/trade/mail degrade with it, the same posture as a
//     failed listener build.
func Register(inj do.Injector, natsCfg config.NATSConfig, log *slog.Logger) func() {
	if natsCfg.Economy == "remote" {
		nc, err := natsinfra.New(natsCfg)
		if err != nil {
			log.Error("economy remote dial failed; economy will not resolve", "err", err)
			return nil
		}
		do.Provide(inj, func(_ do.Injector) (Service, error) {
			return remote.NewProxy(nc, natsCfg.RequestTimeout), nil
		})
		return func() { _ = natsinfra.Close(nc) }
	}

	// Local: resolve the character repo and the zeny ledger from DI (economy
	// writes zeny via the char table and appends to the ledger). Production
	// composition.go provides the GORMLedger before this provider runs.
	do.Provide(inj, func(i do.Injector) (Service, error) {
		repo := do.MustInvoke[chardomain.CharacterRepository](i)
		var ledger economydomain.LedgerRepository
		if l, err := do.Invoke[economydomain.LedgerRepository](i); err == nil {
			ledger = l
		}
		return app.NewEconomyService(repo, ledger), nil
	})
	return nil
}
