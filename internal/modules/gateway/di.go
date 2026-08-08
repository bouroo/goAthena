// Package gateway is the ingress module root. NewLoginServer resolves the
// account Authenticator from the injector and builds the login TCP listener.
package gateway

import (
	"fmt"
	"log/slog"

	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/modules/account/domain"
	"github.com/bouroo/goAthena/internal/modules/gateway/app"
)

// NewLoginServer resolves the account Authenticator and builds the login
// listener. Called from the composition root after account registers.
func NewLoginServer(inj do.Injector, cfg config.Config, log *slog.Logger) (*app.LoginServer, error) {
	auth, err := do.Invoke[domain.Authenticator](inj)
	if err != nil {
		return nil, fmt.Errorf("resolve authenticator: %w", err)
	}
	ls, err := app.NewLoginServer(
		auth,
		log,
		cfg.Gateway.LoginHost,
		cfg.App.Name,
		uint16(cfg.Gateway.CharPort), //nolint:gosec // G115: CharPort is operator-set (default 6121), bounded to port range.
	)
	if err != nil {
		return nil, fmt.Errorf("login server: %w", err)
	}
	return ls, nil
}
