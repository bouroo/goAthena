// Package metrics holds the process's custom Prometheus instruments. The
// default registry already carries Go runtime and process metrics (exposed by
// /metrics); this package adds the game-protocol counters.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// UnknownOpcodes counts inbound frames that had no wired handler, by listener
// and by whether the packet DB knows the opcode.
//
// The `known="true"` series is a coverage signal: the DB defines the verb but
// no handler is wired yet (drop/trade/skill today), which a normal client
// sends. The `known="false"` series is the security signal — the client sent an
// opcode this server has never heard of, which a playing client does not do and
// a port scan or fuzzer does.
var UnknownOpcodes = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "goathena",
	Subsystem: "gateway",
	Name:      "unknown_opcodes_total",
	Help:      "Inbound frames skipped because their opcode has no wired handler, by whether the packet DB defines the opcode.",
}, []string{"listener", "known"})
