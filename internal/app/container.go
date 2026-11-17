package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	httpadapter "github.com/Orang2-BU/pantara-back/internal/adapters/inbound/http"
	"github.com/Orang2-BU/pantara-back/internal/adapters/outbound/ai"
	"github.com/Orang2-BU/pantara-back/internal/adapters/outbound/cache"
	"github.com/Orang2-BU/pantara-back/internal/adapters/outbound/mailer"
	"github.com/Orang2-BU/pantara-back/internal/adapters/outbound/security"
	"github.com/Orang2-BU/pantara-back/internal/adapters/outbound/sqlstore"
	"github.com/Orang2-BU/pantara-back/internal/application/port"
	"github.com/Orang2-BU/pantara-back/internal/application/usecase"
	"github.com/Orang2-BU/pantara-back/internal/config"
	"github.com/Orang2-BU/pantara-back/internal/seeddata"
)

// Container wires all outbound adapters, application use cases, and the inbound HTTP router cleanly.
type Container struct {
	Store         *sqlstore.Store
	Security      *cache.SecurityStore
	AuthUC        *usecase.AuthUseCase
	WorkspaceUC   *usecase.WorkspaceUseCase
	CatalogUC     *usecase.CatalogUseCase
	TaskUC        *usecase.TaskUseCase
	SelfUC        *usecase.SelfUseCase
	ReportNotifUC *usecase.ReportAndNotifUseCase
	Handler       http.Handler
	Clock         port.Clock
	Hasher        port.PasswordHasher
}

// OpenStore initializes either PostgreSQL (pgxpool) or SQLite depending on cfg.DatabaseURL.
func OpenStore(ctx context.Context, cfg config.Config) (*sqlstore.Store, error) {
	if cfg.IsPostgres() {
		return sqlstore.OpenPostgres(ctx, cfg.DatabaseURL, cfg.DBMaxConns, cfg.DBMinConns)
	}
	return sqlstore.OpenSQLite(cfg.SQLiteDSN())
}

// NewContainer builds a fully-wired dependency graph around store (usable both in main and tests).
func NewContainer(cfg config.Config, store *sqlstore.Store, clock port.Clock, log *slog.Logger) *Container {
	if clock == nil {
		clock = security.RealClock{}
	}
	hasher := security.Argon2Hasher{}
	tokens := security.NewJWTTokenService(cfg.JWTSecret)
	secStore := cache.NewSecurityStore(cfg.RedisURL)
	mlr := &mailer.DevSMTPMailer{
		Host: cfg.SMTPHost,
		Port: strconv.Itoa(cfg.SMTPPort),
		User: cfg.SMTPUser,
		Pass: cfg.SMTPPass,
		From: cfg.SMTPFrom,
		Log:  log,
	}
	analyzer := ai.NewHybridTaskDNAnalyzer(cfg.AIProviderURL, cfg.AIAPIKey, cfg.AIModel, cfg.AITimeout, secStore)

	comps := sqlstore.CompetencyRepo{Store: store}
	members := sqlstore.MemberRepo{Store: store}
	projects := sqlstore.ProjectRepo{Store: store}
	tasks := sqlstore.TaskRepo{Store: store}
	decs := sqlstore.DecisionRepo{Store: store}
	notifs := sqlstore.NotificationRepo{Store: store}

	authUC := usecase.NewAuthUseCase(store, store, store, secStore, hasher, tokens, mlr, clock, cfg.AppBaseURL)
	wsUC := usecase.NewWorkspaceUseCase(store, store, comps, members, projects, tasks, decs, store, tokens, mlr, clock, cfg.AppBaseURL)
	catUC := usecase.NewCatalogUseCase(store, store, comps, members, projects, tasks, store, notifs, tokens, mlr, secStore, clock, cfg.AppBaseURL)
	taskUC := usecase.NewTaskUseCase(store, store, comps, members, projects, tasks, decs, store, notifs, analyzer, clock)
	selfUC := usecase.NewSelfUseCase(store, store, members, tasks, store, notifs, clock)
	repUC := usecase.NewReportAndNotifUseCase(store, comps, members, projects, tasks, decs, notifs, clock)

	handler := httpadapter.NewRouter(httpadapter.RouterDeps{
		AuthUC:             authUC,
		WorkspaceUC:        wsUC,
		CatalogUC:          catUC,
		TaskUC:             taskUC,
		SelfUC:             selfUC,
		ReportNotifUC:      repUC,
		Security:           secStore,
		HealthCheck:        store.Ping,
		CORSAllowedOrigins: cfg.CORSAllowedOrigins,
		CookieSecure:       cfg.CookieSecure,
		CookieSecureAuto:   cfg.CookieSecureAuto,
		CookieDomain:       cfg.CookieDomain,
		IncludeDevTokens:   cfg.AppEnv != "production",
		Logger:             log,
	})

	return &Container{
		Store:         store,
		Security:      secStore,
		AuthUC:        authUC,
		WorkspaceUC:   wsUC,
		CatalogUC:     catUC,
		TaskUC:        taskUC,
		SelfUC:        selfUC,
		ReportNotifUC: repUC,
		Handler:       handler,
		Clock:         clock,
		Hasher:        hasher,
	}
}

// SeedIfEmpty seeds the canonical Arunika Digital demo workspace when zero organizations exist.
func (c *Container) SeedIfEmpty(ctx context.Context, fixedDates bool) (bool, error) {
	return seeddata.SeedArunika(ctx, seeddata.SeedDeps{
		Store:    c.Store,
		Members:  sqlstore.MemberRepo{Store: c.Store},
		Projects: sqlstore.ProjectRepo{Store: c.Store},
		Tasks:    sqlstore.TaskRepo{Store: c.Store},
		Decs:     sqlstore.DecisionRepo{Store: c.Store},
		Notifs:   sqlstore.NotificationRepo{Store: c.Store},
		Hasher:   c.Hasher,
		Clock:    c.Clock,
	}, seeddata.SeedOptions{FixedDates: fixedDates})
}

// Close shuts down Redis and database pools gracefully.
func (c *Container) Close() error {
	var err error
	if c.Security != nil {
		if rErr := c.Security.Close(); rErr != nil {
			err = fmt.Errorf("close redis: %w", rErr)
		}
	}
	if c.Store != nil {
		if sErr := c.Store.Close(); sErr != nil && err == nil {
			err = sErr
		}
	}
	return err
}
