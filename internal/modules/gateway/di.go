// Package gateway is the ingress module root. It builds the login and char TCP
// listeners, resolving the account Authenticator, character CharService, and
// SessionStore ports from the injector.
package gateway

import (
	"fmt"
	"log/slog"

	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/modules/account/domain"
	charapp "github.com/bouroo/goAthena/internal/modules/character/app"
	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	shopapp "github.com/bouroo/goAthena/internal/modules/commerce/shop/app"
	contentapp "github.com/bouroo/goAthena/internal/modules/content/app"
	contentdomain "github.com/bouroo/goAthena/internal/modules/content/domain"
	"github.com/bouroo/goAthena/internal/modules/gateway/app"
	invapp "github.com/bouroo/goAthena/internal/modules/inventory/app"
	worldapp "github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	"github.com/bouroo/goAthena/pkg/ro/skilldb"
)

// NewLoginServer resolves the Authenticator + SessionStore and builds the login
// listener. Called from the composition root after account + character register.
// The login rate limiter is built from cfg.Gateway.LoginRateLimit (capacity +
// per-second refill); a zero capacity or rate returns nil and disables limiting.
func NewLoginServer(inj do.Injector, cfg config.Config, log *slog.Logger) (*app.LoginServer, error) {
	auth, err := do.Invoke[domain.Authenticator](inj)
	if err != nil {
		return nil, fmt.Errorf("resolve authenticator: %w", err)
	}
	sess, err := do.Invoke[chardomain.SessionStore](inj)
	if err != nil {
		return nil, fmt.Errorf("resolve session store: %w", err)
	}
	limiter := app.NewLoginRateLimiter(cfg.Gateway.LoginRateBurst, cfg.Gateway.LoginRatePerSec)
	ls, err := app.NewLoginServer(
		auth,
		sess,
		limiter,
		log,
		cfg.Gateway.CharHost,
		cfg.App.Name,
		uint16(cfg.Gateway.CharPort), //nolint:gosec // G115: CharPort operator-set (default 6121).
	)
	if err != nil {
		return nil, fmt.Errorf("login server: %w", err)
	}
	return ls, nil
}

// NewCharServer resolves the CharService + AccountRepo and builds the char-select listener.
func NewCharServer(inj do.Injector, cfg config.Config, log *slog.Logger) (*app.CharServer, error) {
	chars, err := do.Invoke[*charapp.CharService](inj)
	if err != nil {
		return nil, fmt.Errorf("resolve char service: %w", err)
	}
	accRepo, err := do.Invoke[domain.AccountRepository](inj)
	if err != nil {
		return nil, fmt.Errorf("resolve account repository: %w", err)
	}
	cs, err := app.NewCharServer(
		chars,
		accRepo,
		log,
		cfg.Gateway.MapHost,
		uint16(cfg.Gateway.MapPort), //nolint:gosec // G115: MapPort operator-set (default 5121).
	)
	if err != nil {
		return nil, fmt.Errorf("char server: %w", err)
	}
	return cs, nil
}

// resolveOptional invokes an optional dependency, downgrading a resolve
// failure to a warn + nil so a missing registry degrades feature quality
// instead of refusing startup.
func resolveOptional[T any](inj do.Injector, log *slog.Logger, name string) T {
	v, err := do.Invoke[T](inj)
	if err != nil {
		log.Warn("optional dependency unresolved", "dep", name, "err", err)
		var zero T
		return zero
	}
	return v
}

// NewMapServer resolves the WorldService + SessionStore and builds the map listener.
func NewMapServer(inj do.Injector, log *slog.Logger) (*app.MapServer, error) {
	world, err := do.Invoke[*worldapp.WorldService](inj)
	if err != nil {
		return nil, fmt.Errorf("resolve world service: %w", err)
	}
	spawn, err := do.Invoke[*worldapp.SpawnService](inj)
	if err != nil {
		return nil, fmt.Errorf("resolve spawn service: %w", err)
	}
	combat, err := do.Invoke[*worldapp.CombatService](inj)
	if err != nil {
		return nil, fmt.Errorf("resolve combat service: %w", err)
	}
	equip, err := do.Invoke[*worldapp.EquipService](inj)
	if err != nil {
		return nil, fmt.Errorf("resolve equip service: %w", err)
	}
	itemUse, err := do.Invoke[*worldapp.ItemUseService](inj)
	if err != nil {
		return nil, fmt.Errorf("resolve item-use service: %w", err)
	}
	inv, err := do.Invoke[*invapp.InventoryService](inj)
	if err != nil {
		return nil, fmt.Errorf("resolve inventory service: %w", err)
	}
	sess, err := do.Invoke[chardomain.SessionStore](inj)
	if err != nil {
		return nil, fmt.Errorf("resolve session store: %w", err)
	}
	content, err := do.Invoke[*contentapp.Engine](inj)
	if err != nil {
		return nil, fmt.Errorf("resolve content engine: %w", err)
	}
	skills, err := do.Invoke[*worldapp.SkillService](inj)
	if err != nil {
		return nil, fmt.Errorf("resolve skill service: %w", err)
	}
	skillDB := resolveOptional[*skilldb.Registry](inj, log, "skilldb")
	shops, err := do.Invoke[*shopapp.ShopService](inj)
	if err != nil {
		return nil, fmt.Errorf("resolve shop service: %w", err)
	}
	shopStore, err := do.Invoke[contentdomain.ShopStore](inj)
	if err != nil {
		return nil, fmt.Errorf("resolve shop store: %w", err)
	}
	trade, err := do.Invoke[*worldapp.TradeService](inj)
	if err != nil {
		return nil, fmt.Errorf("resolve trade service: %w", err)
	}
	// Mob AI is best-effort: a resolve failure (no MobAIService provided) leaves
	// mobs passive — the map server still boots, only the aggro/attack loop is
	// inert. Matches the tick-loop's own nil-tolerant resolve of MobAIService.
	var mobAI *worldapp.MobAIService
	if svc, mobErr := do.Invoke[*worldapp.MobAIService](inj); mobErr != nil {
		log.Error("mob AI service resolve failed; mobs will stay passive", "err", mobErr)
	} else {
		mobAI = svc
	}
	ms, err := app.NewMapServer(world, spawn, combat, mobAI, equip, itemUse, inv, content, skills, shops, shopStore, trade, sess, skillDB, log)
	if err != nil {
		return nil, fmt.Errorf("map server: %w", err)
	}
	// item_db resolves the IT_* wire type and View sprite the LoadEndAck
	// inventory burst writes. Attached post-construction (like skillDB above) to
	// keep NewMapServer's positional signature stable; unresolved leaves the
	// burst's entries typed IT_ETC with a 0 sprite rather than refusing startup.
	ms.SetItemDB(resolveOptional[*itemdb.Registry](inj, log, "itemdb"))
	return ms, nil
}
