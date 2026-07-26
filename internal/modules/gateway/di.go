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

	// The gateway multiplexes the login, char, and (at M3) map roles on a single
	// connection: the role advances in-connection rather than the client
	// reconnecting to a separate char server. So the connection's decoder must
	// frame every C→S opcode it will see on that one stream — login (CA_*) and
	// char (CH_*). A login-only DB would reject CH_ENTER (0x0065) as an unknown
	// opcode and drop the connection the moment the client entered the char
	// flow. Merge the char-server C→S set into the login DB once; packet.DB is
	// concurrency-read-safe after construction, so one shared DB backs every
	// connection. The per-version map codec swap lands at M3.
	db := packet.NewLoginServerDB()
	db.Merge(packet.NewCharServerDB())
	newDec := infra.DecoderFactory(func() *netcodec.Decoder {
		return netcodec.NewLoginDecoder(db)
	})
	do.ProvideValue(c, newDec)

	registry, err := do.Invoke[*telemetry.Registry](c)
	if err != nil {
		return fmt.Errorf("gateway: resolve health registry: %w", err)
	}
	registry.AddReadiness(gatewayChecker{
		tcpAddr: cfg.Gateway.TCP.Addr,
		wsAddr:  cfg.Gateway.WS.Addr,
	})

	return nil
}

// readinessDialTimeout bounds the readiness dial so a hung or absent listener
// cannot stall the /readyz probe.
const readinessDialTimeout = time.Second

// gatewayChecker reports ready only when both ingress listeners accept
// connections. It dials rather than checking construction because readiness
// means "can serve game traffic," and the listeners bind inside Run — until
// then the gateway is correctly not-ready. Each dial is a connect-then-close
// with no protocol bytes, which is benign for both the gnet TCP handler
// (OnOpen then OnClose) and the WS http listener.
type gatewayChecker struct {
	tcpAddr string
	wsAddr  string
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
	return dial(g.wsAddr)
}
