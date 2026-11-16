package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"time"

	"github.com/Orang2-BU/pantara-back/internal/adapters/outbound/security"
	"github.com/Orang2-BU/pantara-back/internal/app"
	"github.com/Orang2-BU/pantara-back/internal/config"
)

func main() {
	fixedDates := flag.Bool("fixed-dates", false, "Preserve literal 2026-09-28..2026-10-02 seed dates instead of shifting to current week")
	flag.Parse()

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

	container := app.NewContainer(cfg, store, security.RealClock{}, log)
	defer container.Close()

	seeded, err := container.SeedIfEmpty(ctx, *fixedDates)
	if err != nil {
		log.Error("seed failed", "err", err)
		os.Exit(1)
	}
	if seeded {
		log.Info("seeded Arunika Digital workspace cleanly", "fixedDates", *fixedDates)
	} else {
		log.Info("seed skipped: organizations already exist")
	}
}
