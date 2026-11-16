package sqlstore

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/Orang2-BU/pantara-back/internal/domain/errs"
)

//go:embed migrations/postgres/*.sql migrations/sqlite/*.sql
var migrationFS embed.FS

type Dialect string

const (
	DialectPostgres Dialect = "postgres"
	DialectSQLite   Dialect = "sqlite"
)

type txContextKey struct{}

// DBExecutor is satisfied by both *sql.DB and *sql.Tx.
type DBExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Store wraps SQL database access, dialect rebinding, atomic TxManager, and pgxpool health checks.
type Store struct {
	db      *sql.DB
	pool    *pgxpool.Pool
	dialect Dialect
	txMu    sync.Mutex // serializes explicit transactions on SQLite :memory: under concurrency
}

var pgPlaceholderRe = regexp.MustCompile(`\$\d+`)

// OpenPostgres initializes a tuned pgxpool (sized for 1000 concurrent users) and bridges it cleanly.
func OpenPostgres(ctx context.Context, dsn string, maxConns, minConns int32) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse pg dsn: %w", err)
	}
	if maxConns <= 0 {
		maxConns = 80
	}
	if minConns <= 0 {
		minConns = 15
	}
	cfg.MaxConns = maxConns
	cfg.MinConns = minConns
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	cfg.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "10000"

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect pg pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping pg: %w", err)
	}
	db := stdlib.OpenDBFromPool(pool)
	return &Store{db: db, pool: pool, dialect: DialectPostgres}, nil
}

// OpenSQLite opens a pure-Go SQLite connection (e.g., ":memory:" for isolated tests).
func OpenSQLite(dsn string) (*Store, error) {
	if dsn == "" || dsn == ":memory:" {
		dsn = "file::memory:?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Single connection keeps :memory: database shared reliably across concurrent test goroutines.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite pragmas: %w", err)
	}
	return &Store{db: db, dialect: DialectSQLite}, nil
}

func (s *Store) Close() error {
	var err error
	if s.db != nil {
		err = s.db.Close()
	}
	if s.pool != nil {
		s.pool.Close()
	}
	return err
}

func (s *Store) Ping(ctx context.Context) error {
	if s.pool != nil {
		return s.pool.Ping(ctx)
	}
	return s.db.PingContext(ctx)
}

// Migrate applies all embedded SQL migrations matching the active dialect idempotently.
func (s *Store) Migrate(ctx context.Context) error {
	dir := "migrations/postgres"
	if s.dialect == DialectSQLite {
		dir = "migrations/sqlite"
	}
	entries, err := fs.ReadDir(migrationFS, dir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		raw, err := migrationFS.ReadFile(dir + "/" + name)
		if err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, string(raw)); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := s.db.ExecContext(ctx, s.q("INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT DO NOTHING"), name); err != nil {
			return fmt.Errorf("record migration %s: %w", name, err)
		}
	}
	return nil
}

// WithinTx executes fn inside an atomic database transaction, nesting cleanly if already inside one.
func (s *Store) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, already := ctx.Value(txContextKey{}).(*sql.Tx); already {
		return fn(ctx)
	}
	if s.dialect == DialectSQLite {
		s.txMu.Lock()
		defer s.txMu.Unlock()
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return errs.Internal("gagal memulai transaksi database", err)
	}
	txCtx := context.WithValue(ctx, txContextKey{}, tx)
	if err := fn(txCtx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return errs.Internal("gagal menyimpan transaksi database", err)
	}
	return nil
}

func (s *Store) exec(ctx context.Context) DBExecutor {
	if tx, ok := ctx.Value(txContextKey{}).(*sql.Tx); ok && tx != nil {
		return tx
	}
	return s.db
}

// q converts PostgreSQL $1..$n parameter placeholders to ? when running on SQLite.
func (s *Store) q(query string) string {
	if s.dialect == DialectPostgres {
		return query
	}
	return pgPlaceholderRe.ReplaceAllString(query, "?")
}

func (s *Store) forUpdate() string {
	if s.dialect == DialectPostgres {
		return " FOR UPDATE"
	}
	return ""
}

func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
