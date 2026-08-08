// Package app is the composition root: it wires the modular monolith's process
// from config (logger → HTTP control plane → feature modules). Each bounded
// context is added here behind an interface so wiring stays auditable and
// split-ready. Game-protocol listeners (M1+) land in later phases.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/bouroo/goAthena/internal/config"
)

// App is the assembled modular monolith.
type App struct {
	cfg    *config.Config
	log    *slog.Logger
	server *http.Server
	ready  atomic.Bool
}

// New builds the App from configuration. It constructs the HTTP control plane
// (health/metrics/ops). Feature modules register their listeners in later
// phases and flip readiness through SetReady.
func New(cfg *config.Config, log *slog.Logger) (*App, error) {
	a := &App{cfg: cfg, log: log}
	addr := fmt.Sprintf("%s:%d", cfg.HTTP.Host, cfg.HTTP.Port)
	a.server = &http.Server{
		Addr:         addr,
		Handler:      a.routes(),
		ReadTimeout:  cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout,
		IdleTimeout:  cfg.HTTP.IdleTimeout,
	}
	// The control plane is ready as soon as it can serve; game modules raise the
	// bar in later phases.
	a.ready.Store(true)
	return a, nil
}

// SetReady toggles process readiness. Feature modules call SetReady(false)
// while a dependency is down so load balancers stop sending traffic.
func (a *App) SetReady(ok bool) { a.ready.Store(ok) }

// Run serves the control plane until ctx is cancelled, then shuts down within
// the configured grace period.
func (a *App) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		a.log.Info("http control plane listening", "addr", a.server.Addr)
		if err := a.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("http serve: %w", err)
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		a.log.Info("shutdown signal received, draining", "timeout", a.cfg.App.ShutdownTimeout)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.cfg.App.ShutdownTimeout)
	defer cancel()

	if err := a.server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	a.log.Info("shutdown complete")
	return nil
}

// routes wires the control-plane HTTP mux. /healthz is liveness (process up);
// /readyz is readiness (dependencies up). Both are cheap, allocation-free.
func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !a.ready.Load() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	mux.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"version":%q,"commit":%q,"build_time":%q}`, Version, Commit, BuildTime)
	})
	return mux
}
