// Package gateway is the composition point for the gateway bounded context's
// DI. It builds the transport-agnostic dispatch core and the per-connection
// codec factory from the handler contributions the composition root provides,
// and registers a readiness probe over the configured listeners. Like the
// account module's di.go, this file lives at the module root so it may import
// both its own app and infra layers.
package gateway

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/config"
	netcodec "github.com/bouroo/goAthena/internal/infrastructure/net"
	gwapp "github.com/bouroo/goAthena/internal/modules/gateway/app"
	"github.com/bouroo/goAthena/internal/modules/gateway/infra"
	"github.com/bouroo/goAthena/internal/shared/telemetry"
	rocrypto "github.com/bouroo/goAthena/pkg/ro/crypto"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// Register builds the gateway dispatch core and the per-connection codec
// factory, and registers a readiness probe. It does NOT start the listeners:
// they run under the Application's shutdown-governing context, which is only
// available at Run time, so the composition root constructs the TCP/WS servers
// against the *domain.Dispatcher and infra.DecoderFactory provided here and
// registers them as Runnables.
func Register(_ context.Context, c do.Injector) error {
	if _, err := do.Invoke[*zerolog.Logger](c); err != nil {
		return fmt.Errorf("gateway: resolve logger: %w", err)
	}
	cfg, err := do.Invoke[*config.Config](c)
	if err != nil {
		return fmt.Errorf("gateway: resolve config: %w", err)
	}

	// The handler contributions come from the composition root (it alone may
	// import the feature modules); build the immutable dispatcher from them.
	handlers, err := do.Invoke[gwapp.Handlers](c)
	if err != nil {
		return fmt.Errorf("gateway: resolve handler contributions: %w", err)
	}
	disp := gwapp.BuildDispatcher(handlers)
	do.ProvideValue(c, disp)

	// Two listeners back the gateway, each with its own codec:
	//
	//   - The login/char listener multiplexes login (CA_*) and char (CH_*) on one
	//     connection — the role advances RoleLogin → RoleChar in-connection, so its
	//     decoder must frame both opcode sets. A login-only DB would reject
	//     CH_ENTER (0x0065) as an unknown opcode and drop the connection the
	//     moment the client entered the char flow. Merge the char C→S set into the
	//     login DB once; packet.DB is concurrency-read-safe after construction, so
	//     one shared DB backs every login/char connection.
	//
	//   - The map listener serves the fresh connections HC_NOTIFY_ZONESVR
	//     redirects to after CH_SELECT_CHAR (a reconnect, not an in-connection role
	//     advance). Its decoder uses map framing, keyed by the obfuscation triplet
	//     for the configured PACKETVER. For Thai Classic (20250604)
	//     crypto.KeysForVersion returns (0,0,0) — kRO dropped obfuscation after the
	//     cutoff — so the map decoder is an identity transform; only the framing
	//     differs from the login decoder.
	loginDB := packet.NewLoginServerDB()
	loginDB.Merge(packet.NewCharServerDB())
	newLoginDec := infra.DecoderFactory(func() *netcodec.Decoder {
		return netcodec.NewLoginDecoder(loginDB)
	})
	do.ProvideValue(c, newLoginDec)

	mapDB := packet.NewMapServerDB()
	key0, key1, key2 := rocrypto.KeysForVersion(cfg.Gateway.Packetver)
	newMapDec := infra.MapDecoderFactory(func() *netcodec.Decoder {
		return netcodec.NewMapDecoder(mapDB, key0, key1, key2)
	})
	do.ProvideValue(c, newMapDec)

	registry, err := do.Invoke[*telemetry.Registry](c)
	if err != nil {
		return fmt.Errorf("gateway: resolve health registry: %w", err)
	}
	registry.AddReadiness(gatewayChecker{
		tcpAddr: cfg.Gateway.TCP.Addr,
		wsAddr:  cfg.Gateway.WS.Addr,
		// Dial the address the map listener actually binds to, not MapAddr:
		// when MapBindAddr splits bind from advertise (NAT/Docker
		// deployments), the advertised address is not guaranteed to be
		// dialable from inside this process's network namespace.
		mapAddr: cfg.Gateway.MapListenAddr(),
	})

	return nil
}

// readinessDialTimeout bounds the readiness dial so a hung or absent listener
// cannot stall the /readyz probe.
const readinessDialTimeout = time.Second

// gatewayChecker reports ready only when every ingress listener accepts
// connections: the login/char TCP and WS listeners plus the map TCP listener.
// It dials rather than checking construction because readiness means "can serve
// game traffic," and the listeners bind inside Run — until then the gateway is
// correctly not-ready. Each dial is a connect-then-close with no protocol bytes,
// which is benign for both the gnet TCP handler (OnOpen then OnClose) and the WS
// http listener.
type gatewayChecker struct {
	tcpAddr string
	wsAddr  string
	mapAddr string
}

func (g gatewayChecker) Name() string { return "gateway-listeners" }

func (g gatewayChecker) Check(ctx context.Context) error {
	dial := func(addr string) error {
		d := net.Dialer{Timeout: readinessDialTimeout}
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("dial %s: %w", addr, err)
		}
		return conn.Close()
	}
	if err := dial(g.tcpAddr); err != nil {
		return err
	}
	if err := dial(g.wsAddr); err != nil {
		return err
	}
	return dial(g.mapAddr)
}
