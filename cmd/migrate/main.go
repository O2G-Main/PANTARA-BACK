package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/Orang2-BU/pantara-back/internal/app"
	"github.com/Orang2-BU/pantara-back/internal/config"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.Load()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	store, err := app.OpenStore(ctx, cfg)
	if err != nil {
		log.Error("open store failed", "err", err)
		os.Exit(1)
	}
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		log.Error("migrate failed", "err", err)
		os.Exit(1)
	}
	log.Info("migrations applied cleanly", "postgres", cfg.IsPostgres())
}
