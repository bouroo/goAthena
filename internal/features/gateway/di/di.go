// Package di wires the gateway feature into the DI container.
package di

import (
	"fmt"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	identityv1 "github.com/bouroo/goAthena/api/pb/identity/v1"
	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/features/gateway/domain"
	"github.com/bouroo/goAthena/internal/features/gateway/handler"
	"github.com/bouroo/goAthena/internal/features/gateway/service"
	natsinfra "github.com/bouroo/goAthena/internal/infrastructure/messaging/nats"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// Register wires the gateway feature (packet codec, identity gRPC
// client, zone gRPC client, dispatch handler, TCP/WS ingress) into the
// DI container.
//
// Resolved dependencies (single instances, lazy on first Invoke):
//   - *grpc.ClientConn: a lazy connection to the identity service.
//   - identityv1.IdentityServiceClient: the typed client built on the
//     connection above.
//   - zonev1.ZoneServiceClient: the typed client for forwarding
//     map-server packets (CZ_ENTER, CZ_REQUEST_MOVE) to the zone
//     service.
//   - *packet.DB: the merged login-server + char-server + map-server
//     packet database.
//   - domain.PacketHandler: M3b dispatch handler that handles CA_LOGIN,
//     CH_ENTER, CH_SELECT_CHAR, CZ_ENTER, and CZ_REQUEST_MOVE.
//   - *handler.TCPHandler: the gnet EventHandler for kRO TCP ingress.
//   - *handler.WSHandler: the HTTP/WebSocket upgrade handler for the
//     roBrowser ingress.
func Register(c do.Injector) error {
	do.Provide(c, func(_ do.Injector) (*packet.DB, error) {
		// Merge char-server and map-server packet defs into the
		// login-server DB so the codec can decode every packet the
		// rAthena handshake (CA_LOGIN → CH_ENTER → CH_SELECT_CHAR →
		// CZ_ENTER) emits on the wire without the dispatch layer
		// caring which side they're on.
		db := packet.NewLoginServerDB()
		db.Merge(packet.NewCharServerDB())
		db.Merge(packet.NewMapServerDB())
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

	// Zone gRPC client — built on its own lazy connection. We do NOT
	// register a second *grpc.ClientConn in the DI container because
	// the identity connection already occupies that type slot (samber/
	// do/v2 stores providers by exact type, not by parameter), so the
	// zone client opens its connection inline. The cost is a second
	// grpc.ClientConn per process; both use idle transport so the
	// memory delta is negligible.
	do.Provide(c, buildZoneClient)

	// Single in-process session registry shared by the dispatch
	// handler (Register / SetView) and the TCP/WS handlers
	// (Unregister on close). The future NATS broadcast subscriber
	// (a later workstream) will resolve the same SessionRegistry
	// instance via do.Invoke[service.SessionRegistry].
	do.Provide(c, func(_ do.Injector) (service.SessionRegistry, error) {
		return service.NewSessionRegistry(), nil
	})

	// Broadcast subscriber: subscribes to zone.event.> over NATS and fans
	// movement/spawn/vanish events out to observer sessions. The same
	// instance backs the dispatch handler's on-enter area-spawn path via
	// SetAreaSender. Requires *natsinfra.Client (registered by the app
	// composition root via nats.Register) — lazy, so Register itself does
	// not connect.
	do.Provide(c, func(i do.Injector) (*service.BroadcastSubscriber, error) {
		nc, err := do.Invoke[*natsinfra.Client](i)
		if err != nil {
			return nil, fmt.Errorf("resolve nats client for broadcast: %w", err)
		}
		registry, err := do.Invoke[service.SessionRegistry](i)
		if err != nil {
			return nil, err
		}
		logger, err := do.Invoke[*zerolog.Logger](i)
		if err != nil {
			return nil, err
		}
		return service.NewBroadcastSubscriber(nc, registry, *logger), nil
	})

	do.Provide(c, provideDispatchHandler)
	do.Provide(c, provideTCPHandler)
	do.Provide(c, provideWSHandler)

	return nil
}

// buildZoneClient constructs a lazy zone gRPC client. Extracted from
// Register to keep the gocyclo budget under 15.
func buildZoneClient(i do.Injector) (zonev1.ZoneServiceClient, error) {
	cfg, err := do.Invoke[*config.Config](i)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(cfg.Gateway.ZoneAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial zone gRPC at %s: %w", cfg.Gateway.ZoneAddr, err)
	}
	return zonev1.NewZoneServiceClient(conn), nil
}

// provideDispatchHandler wires the M3b dispatch handler from the DI
// container. Extracted from Register to keep the gocyclo budget
// under 15 after the registry dependency was added in Step 2c.
func provideDispatchHandler(i do.Injector) (domain.PacketHandler, error) {
	identityClient, err := do.Invoke[identityv1.IdentityServiceClient](i)
	if err != nil {
		return nil, err
	}
	zoneClient, err := do.Invoke[zonev1.ZoneServiceClient](i)
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
	registry, err := do.Invoke[service.SessionRegistry](i)
	if err != nil {
		return nil, err
	}
	h, err := buildDispatchHandler(identityClient, zoneClient, cfg, *logger, registry)
	if err != nil {
		return nil, err
	}
	// Wire the broadcast area-spawner onto the dispatch handler.
	// Best-effort: a unit-test DI container that omits nats leaves the
	// handler with a nil area sender (the call-site guards nil). Production
	// always has the broadcaster — app.go registers nats before invoking
	// the TCP/WS handlers.
	if bs, bsErr := do.Invoke[*service.BroadcastSubscriber](i); bsErr == nil {
		h.SetAreaSender(bs)
	} else {
		logger.Warn().Err(bsErr).Msg("gateway di: broadcast subscriber not resolved; on-enter area spawn disabled")
	}
	return h, nil
}

// provideTCPHandler wires the gnet TCP handler from the DI
// container. Extracted from Register to keep the gocyclo budget
// under 15 after the registry dependency was added in Step 2c.
func provideTCPHandler(i do.Injector) (*handler.TCPHandler, error) {
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
	registry, err := do.Invoke[service.SessionRegistry](i)
	if err != nil {
		return nil, err
	}
	return handler.NewTCPHandler(db, pktHandler, registry, *logger), nil
}

// provideWSHandler wires the WebSocket handler from the DI
// container. Extracted from Register to keep the gocyclo budget
// under 15 after the registry dependency was added in Step 2c.
func provideWSHandler(i do.Injector) (*handler.WSHandler, error) {
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
	registry, err := do.Invoke[service.SessionRegistry](i)
	if err != nil {
		return nil, err
	}
	return handler.NewWSHandler(db, pktHandler, registry, cfg.Gateway.WS.Addr, cfg.Gateway.WS.Path, *logger, cfg.Gateway.WS.AllowedOrigins), nil
}

// buildDispatchHandler wires the M3b dispatch handler from resolved
// config + identity client + zone client + logger. Extracted from
// Register to keep the gocyclo budget under 15; the host→IPv4
// resolution step is the only piece that can fail at startup, so it
// bubbles up as a wrapped error that surfaces a misconfigured
// gateway.map_addr immediately.
func buildDispatchHandler(
	identityClient identityv1.IdentityServiceClient,
	zoneClient zonev1.ZoneServiceClient,
	cfg *config.Config,
	logger zerolog.Logger,
	registry service.SessionRegistry,
) (*service.DispatchHandler, error) {
	zoneHost, zonePort, err := service.SplitMapAddr(cfg.Gateway.MapAddr)
	if err != nil {
		return nil, fmt.Errorf("split gateway.map_addr %q: %w", cfg.Gateway.MapAddr, err)
	}
	zoneIP, err := service.ResolveZoneIPv4(zoneHost)
	if err != nil {
		return nil, fmt.Errorf("resolve gateway.map_addr host %q: %w", zoneHost, err)
	}
	return service.NewDispatchHandler(
		identityClient,
		zoneClient,
		cfg.Gateway.Packetver,
		logger,
		cfg.Zone.DefaultMap,
		zoneIP,
		zonePort,
		registry,
	), nil
}
