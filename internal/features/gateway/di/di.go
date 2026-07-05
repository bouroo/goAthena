// Package di wires the gateway feature into the DI container.
package di

import (
	"github.com/rs/zerolog"
	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/features/gateway/domain"
	"github.com/bouroo/goAthena/internal/features/gateway/handler"
	"github.com/bouroo/goAthena/internal/features/gateway/service"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// Register wires the gateway feature (packet codec, TCP/WS ingress,
// PacketHandler stub) into the DI container.
//
// Resolved dependencies (single instances, lazy on first Invoke):
//   - *packet.DB: the login-server packet database (P1.1).
//   - domain.PacketHandler: Phase 1 logging stub; Phase 2+ will replace
//     this with a gRPC-forwarding implementation.
//   - *handler.TCPHandler: the gnet EventHandler for kRO TCP ingress.
//   - *handler.WSHandler: the HTTP/WebSocket upgrade handler for the
//     roBrowser ingress (P1.5).
func Register(c do.Injector) error {
	do.Provide(c, func(_ do.Injector) (*packet.DB, error) {
		return packet.NewLoginServerDB(), nil
	})

	do.Provide(c, func(i do.Injector) (domain.PacketHandler, error) {
		logger, err := do.Invoke[*zerolog.Logger](i)
		if err != nil {
			return nil, err
		}
		return service.NewLoggingHandler(*logger), nil
	})

	do.Provide(c, func(i do.Injector) (*handler.TCPHandler, error) {
		db, err := do.Invoke[*packet.DB](i)
		if err != nil {
			return nil, err
		}
		pktHandler, err := do.Invoke[domain.PacketHandler](i)
		if err != nil {
			return nil, err
		}
		logger, err := do.Invoke[*zerolog.Logger](i)
		if err != nil {
			return nil, err
		}
		return handler.NewTCPHandler(db, pktHandler, *logger), nil
	})

	do.Provide(c, func(i do.Injector) (*handler.WSHandler, error) {
		db, err := do.Invoke[*packet.DB](i)
		if err != nil {
			return nil, err
		}
		pktHandler, err := do.Invoke[domain.PacketHandler](i)
		if err != nil {
			return nil, err
		}
		logger, err := do.Invoke[*zerolog.Logger](i)
		if err != nil {
			return nil, err
		}
		cfg, err := do.Invoke[*config.Config](i)
		if err != nil {
			return nil, err
		}
		return handler.NewWSHandler(db, pktHandler, cfg.Gateway.WS.Addr, cfg.Gateway.WS.Path, *logger), nil
	})

	return nil
}
