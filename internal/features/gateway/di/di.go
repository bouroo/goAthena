// Package di wires the gateway feature into the DI container.
package di

import (
	"fmt"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	identityv1 "github.com/bouroo/goAthena/api/pb/identity/v1"
	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/features/gateway/domain"
	"github.com/bouroo/goAthena/internal/features/gateway/handler"
	"github.com/bouroo/goAthena/internal/features/gateway/service"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// Register wires the gateway feature (packet codec, identity gRPC
// client, dispatch handler, TCP/WS ingress) into the DI container.
//
// Resolved dependencies (single instances, lazy on first Invoke):
//   - *grpc.ClientConn: a lazy connection to the identity service.
//   - identityv1.IdentityServiceClient: the typed client built on the
//     connection above.
//   - *packet.DB: the merged login-server + char-server packet database.
//   - domain.PacketHandler: M2b dispatch handler that handles CA_LOGIN,
//     CH_ENTER, and CH_SELECT_CHAR.
//   - *handler.TCPHandler: the gnet EventHandler for kRO TCP ingress.
//   - *handler.WSHandler: the HTTP/WebSocket upgrade handler for the
//     roBrowser ingress.
func Register(c do.Injector) error {
	do.Provide(c, func(_ do.Injector) (*packet.DB, error) {
		// Merge char-server packet defs into the login-server DB so the
		// codec can decode HC_ACCEPT_ENTER / HC_NOTIFY_ZONESVR / etc.
		// on the wire without the dispatch layer caring which side
		// they're on.
		db := packet.NewLoginServerDB()
		db.Merge(packet.NewCharServerDB())
		return db, nil
	})

	do.Provide(c, func(i do.Injector) (*grpc.ClientConn, error) {
		cfg, err := do.Invoke[*config.Config](i)
		if err != nil {
			return nil, err
		}
		conn, err := grpc.NewClient(cfg.Gateway.IdentityAddr,
			grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, fmt.Errorf("dial identity gRPC at %s: %w", cfg.Gateway.IdentityAddr, err)
		}
		return conn, nil
	})

	do.Provide(c, func(i do.Injector) (identityv1.IdentityServiceClient, error) {
		conn, err := do.Invoke[*grpc.ClientConn](i)
		if err != nil {
			return nil, err
		}
		return identityv1.NewIdentityServiceClient(conn), nil
	})

	do.Provide(c, func(i do.Injector) (domain.PacketHandler, error) {
		identityClient, err := do.Invoke[identityv1.IdentityServiceClient](i)
		if err != nil {
			return nil, err
		}
		cfg, err := do.Invoke[*config.Config](i)
		if err != nil {
			return nil, err
		}
		logger, err := do.Invoke[*zerolog.Logger](i)
		if err != nil {
			return nil, err
		}
		zoneHost, zonePort, err := service.SplitMapAddr(cfg.Gateway.MapAddr)
		if err != nil {
			return nil, fmt.Errorf("split gateway.map_addr %q: %w", cfg.Gateway.MapAddr, err)
		}
		return service.NewDispatchHandler(
			identityClient,
			cfg.Gateway.Packetver,
			*logger,
			cfg.Zone.DefaultMap,
			zoneHost,
			zonePort,
		), nil
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
		return handler.NewWSHandler(db, pktHandler, cfg.Gateway.WS.Addr, cfg.Gateway.WS.Path, *logger, cfg.Gateway.WS.AllowedOrigins), nil
	})

	return nil
}
