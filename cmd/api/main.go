package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Orang2-BU/pantara-back/internal/adapters/outbound/security"
	"github.com/Orang2-BU/pantara-back/internal/app"
	"github.com/Orang2-BU/pantara-back/internal/config"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg := config.Load()

	rootCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	store, err := app.OpenStore(rootCtx, cfg)
	cancel()
	if err != nil {
		log.Error("database_connect_failed", "err", err)
		os.Exit(1)
	}

	migCtx, migCancel := context.WithTimeout(context.Background(), 20*time.Second)
	if err := store.Migrate(migCtx); err != nil {
		migCancel()
		log.Error("database_migrate_failed", "err", err)
		os.Exit(1)
	}
	migCancel()

	container := app.NewContainer(cfg, store, security.RealClock{}, log)
	defer func() {
		if err := container.Close(); err != nil {
			log.Warn("container_close_error", "err", err)
		}
	}()

	if cfg.AppEnv != "production" {
		seedCtx, seedCancel := context.WithTimeout(context.Background(), 20*time.Second)
		seeded, err := container.SeedIfEmpty(seedCtx, false)
		seedCancel()
		if err != nil {
			log.Warn("dev_seed_warning", "err", err)
		} else if seeded {
			log.Info("dev_seed_applied", "org", "arunika-digital")
		}
	}

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           container.Handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("pantara_api_listening", "port", cfg.Port, "env", cfg.AppEnv, "postgres", cfg.IsPostgres())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-errCh:
		log.Error("server_fatal_error", "err", err)
		os.Exit(1)
	case <-sigCtx.Done():
		log.Info("pantara_api_shutting_down")
	}

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Error("server_shutdown_error", "err", err)
		os.Exit(1)
	}
	log.Info("pantara_api_stopped")
}
