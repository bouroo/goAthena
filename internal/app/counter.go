package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/bouroo/goAthena/internal/infrastructure/agones"
	worldapp "github.com/bouroo/goAthena/internal/modules/world/app"
)

// CounterReportInterval bounds how often a shard publishes its player
// count to Agones.
//
// It is a compromise: fast enough that the autoscaler sees a load change
// within a minute, slow enough that the sidecar is not asked on every
// player connect. Agones aggregates the counter across the fleet's
// shards, so a stale count on one shard is corrected by the next report.
const CounterReportInterval = 10 * time.Second

// PlayerCounterSource is the surface the counter loop reads: the number
// of players currently connected to this shard.
type PlayerCounterSource interface {
	// OnlinePlayerCount reports how many player characters are connected.
	OnlinePlayerCount() int
}

// playerCounterSource adapts the world service to PlayerCounterSource.
type playerCounterSource struct {
	world *worldapp.WorldService
}

// OnlinePlayerCount reports the world's live player count.
func (p playerCounterSource) OnlinePlayerCount() int {
	if p.world == nil {
		return 0
	}
	return p.world.OnlinePlayerCount()
}

// RunCounterLoop publishes the shard's player count to Agones until ctx
// is done (no-op when not running as an Agones GameServer).
//
// This is what lets the FleetAutoscaler size the fleet by the players
// actually connected, rather than by a buffer of idle shards: Agones
// aggregates the counter across the fleet, and the CounterPolicy divides
// that aggregate by the shard's capacity.
//
// A failed report is logged, not fatal: a sidecar hiccup must not take
// the game server down, and the next report corrects the count.
func RunCounterLoop(ctx context.Context, log *slog.Logger, reporter agones.CounterReporter, source PlayerCounterSource, interval time.Duration) {
	if reporter == nil {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			count := int64(source.OnlinePlayerCount())
			if count > agones.PlayerCounterCapacity {
				// The sidecar rejects a count above the capacity, and the
				// capacity is the shard's design point. Reporting the
				// capacity is honest: the shard is full.
				log.Warn("agones: player count exceeds the shard capacity; reporting the capacity",
					"count", count, "capacity", agones.PlayerCounterCapacity)
				count = agones.PlayerCounterCapacity
			}
			if err := reporter.ReportCounter(ctx, agones.PlayersCounterName, count, agones.PlayerCounterCapacity); err != nil {
				log.Warn("agones: report player counter", "err", err)
			}
		}
	}
}

// counterReporterFor returns the counter reporter for this process, or
// nil when it does not run as a GameServer.
func counterReporterFor(port agones.Port) agones.CounterReporter {
	if port == nil {
		return nil
	}
	reporter, ok := port.(agones.CounterReporter)
	if !ok {
		return nil
	}
	return reporter
}

// counterLoopSource returns the source the counter loop reads.
func (a *App) counterLoopSource() PlayerCounterSource {
	return playerCounterSource{world: a.deps.world}
}
