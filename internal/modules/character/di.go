// Package character is the composition point for the character bounded
// context's DI. It wires the GORM character repository into the CH_ENTER,
// CH_SELECT_CHAR, and CH_MAKE_CHAR gateway handlers and provides the concrete
// handlers for the composition root to thread into the gateway dispatch tables.
//
// This file lives at the module root rather than under app/ or infra/ because
// it must import both its own app and infra layers. The clean-architecture
// guard (internal/app/arch_test.go) forbids an app-layer file from importing
// infra, but a module-root file is the designated wiring seam and is exempt.
package character

import (
	"context"
	"fmt"

	"github.com/samber/do/v2"
	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/modules/character/app"
	"github.com/bouroo/goAthena/internal/modules/character/infra"
)

// Register builds the character bounded context over the GORM char repository:
// the CH_ENTER (char-list), CH_SELECT_CHAR (zone redirect), and CH_MAKE_CHAR
// (character creation) handlers. It provides the concrete handlers on the
// injector for the composition root to thread into gwapp.Handlers. ctx is
// accepted to match the samber/do v2 Register convention but is unused — the
// repository derives a per-request context from the handler call.
func Register(_ context.Context, c do.Injector) error {
	db, err := do.Invoke[*gorm.DB](c)
	if err != nil {
		return fmt.Errorf("character: resolve gorm db: %w", err)
	}
	chars := infra.NewGORMCharacterRepository(db)

	cfg, err := do.Invoke[*config.Config](c)
	if err != nil {
		return fmt.Errorf("character: resolve config: %w", err)
	}
	zone, err := app.ParseZoneAddr(cfg.Gateway.MapAddr)
	if err != nil {
		return fmt.Errorf("character: parse map addr: %w", err)
	}

	do.ProvideValue(c, app.NewCharEnterHandler(chars))
	do.ProvideValue(c, app.NewCharSelectHandler(chars, zone))
	// MaxChars is the per-account slot ceiling (config Identity.MaxChars,
	// validated [0,15], default 15 = rAthena's MAX_CHARS). The make-char handler
	// rejects slot indices >= it with ErrInvalidSlot.
	do.ProvideValue(c, app.NewCharMakeHandler(chars, uint8(cfg.Identity.MaxChars))) //nolint:gosec // G115: MaxChars is config-validated [0,15]
	return nil
}
