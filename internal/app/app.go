// Package app is the composition root: it wires the modular monolith's process
// from config (logger → infrastructure → control plane → feature modules). Each
// bounded context registers its services into the DI injector; game-protocol
// listeners (M1+) land in later phases and flip readiness.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/bouroo/goAthena/internal/config"
)

// App is the assembled modular monolith.
type App struct {
	cfg      *config.Config
	log      *slog.Logger
	echo     *echo.Echo
	server   *http.Server
	deps     *deps
	closeAll func()
}

// New wires infrastructure and builds the echo control plane from configuration.
// echo.Echo is the router/framework; a stdlib http.Server owns timeouts + graceful
// shutdown (echo v5 delegates serving to the caller).
func New(ctx context.Context, cfg *config.Config, log *slog.Logger) (*App, error) {
	inj, d, closeAll := compose(ctx, cfg, log)
	_ = inj // feature modules resolve from it in M1+

	e := echo.New()
	addr := fmt.Sprintf("%s:%d", cfg.HTTP.Host, cfg.HTTP.Port)
	server := &http.Server{
		Addr:         addr,
		Handler:      e,
		ReadTimeout:  cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout,
		IdleTimeout:  cfg.HTTP.IdleTimeout,
	}

	a := &App{cfg: cfg, log: log, echo: e, server: server, deps: d, closeAll: closeAll}
	a.routes()
	return a, nil
}

// Close releases infrastructure. Safe to call once after Run returns.
func (a *App) Close() { a.closeAll() }

// Run serves the control plane until ctx is cancelled, then drains within the
// configured grace period.
func (a *App) Run(ctx context.Context) error {
	// Game-protocol listeners start before the control plane so they are live
	// by the time /healthz/readyz answer.
	if a.deps.login != nil {
		loginAddr := fmt.Sprintf("tcp://%s:%d", a.cfg.Gateway.LoginHost, a.cfg.Gateway.LoginPort)
		a.deps.login.Start(loginAddr)
	}
	if a.deps.char != nil {
		charAddr := fmt.Sprintf("tcp://%s:%d", a.cfg.Gateway.LoginHost, a.cfg.Gateway.CharPort)
		a.deps.char.Start(charAddr)
	}

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
		a.Close()
		return err
	case <-ctx.Done():
		a.log.Info("shutdown signal received, draining", "timeout", a.cfg.App.ShutdownTimeout)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), a.cfg.App.ShutdownTimeout)
	defer cancel()
	if err := a.server.Shutdown(stopCtx); err != nil {
		a.Close()
		return fmt.Errorf("http shutdown: %w", err)
	}
	a.Close()
	a.log.Info("shutdown complete")
	return nil
}

// routes wires the control-plane endpoints. /healthz is liveness (process up);
// /readyz is readiness (dependencies up); /version prints build metadata.
func (a *App) routes() {
	a.echo.GET("/healthz", func(c *echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	a.echo.GET("/readyz", func(c *echo.Context) error {
		if err := a.deps.ready(c.Request().Context()); err != nil {
			return c.String(http.StatusServiceUnavailable, "not ready: "+err.Error())
		}
		return c.String(http.StatusOK, "ready")
	})
	a.echo.GET("/version", func(c *echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{
			"version": Version, "commit": Commit, "build_time": BuildTime,
		})
	})
}
