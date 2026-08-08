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
	"github.com/bouroo/goAthena/internal/modules/gateway/app"
)

// NewLoginServer resolves the Authenticator + SessionStore and builds the login
// listener. Called from the composition root after account + character register.
func NewLoginServer(inj do.Injector, cfg config.Config, log *slog.Logger) (*app.LoginServer, error) {
	auth, err := do.Invoke[domain.Authenticator](inj)
	if err != nil {
		return nil, fmt.Errorf("resolve authenticator: %w", err)
	}
	sess, err := do.Invoke[chardomain.SessionStore](inj)
	if err != nil {
		return nil, fmt.Errorf("resolve session store: %w", err)
	}
	ls, err := app.NewLoginServer(
		auth,
		sess,
		log,
		cfg.Gateway.LoginHost,
		cfg.App.Name,
		uint16(cfg.Gateway.CharPort), //nolint:gosec // G115: CharPort operator-set (default 6121).
	)
	if err != nil {
		return nil, fmt.Errorf("login server: %w", err)
	}
	return ls, nil
}

// NewCharServer resolves the CharService and builds the char-select listener.
func NewCharServer(inj do.Injector, cfg config.Config, log *slog.Logger) (*app.CharServer, error) {
	chars, err := do.Invoke[*charapp.CharService](inj)
	if err != nil {
		return nil, fmt.Errorf("resolve char service: %w", err)
	}
	cs, err := app.NewCharServer(
		chars,
		log,
		cfg.Gateway.LoginHost,
		uint16(cfg.Gateway.MapPort), //nolint:gosec // G115: MapPort operator-set (default 5121).
	)
	if err != nil {
		return nil, fmt.Errorf("char server: %w", err)
	}
	return cs, nil
}
