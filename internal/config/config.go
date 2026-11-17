package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"
)

const DefaultDevJWTSecret = "pantara-dev-jwt-secret-change-in-production-32bytes-min"

// Config holds runtime configuration for the Pantara API server, migrate, and seed binaries.
type Config struct {
	AppEnv             string
	Port               string
	CORSAllowedOrigins []string

	DatabaseURL string
	DBMaxConns  int32
	DBMinConns  int32

	RedisURL string

	JWTSecret        string
	CookieSecure     bool
	CookieSecureAuto bool
	CookieDomain     string

	AIProviderURL string
	AIAPIKey      string
	AIModel       string
	AITimeout     time.Duration

	SMTPHost   string
	SMTPPort   int
	SMTPUser   string
	SMTPPass   string
	SMTPFrom   string
	AppBaseURL string
}

// Load reads configuration from environment variables and an optional local .env file.
func Load() Config {
	loadDotEnv(".env")

	originsRaw := envOr("CORS_ALLOWED_ORIGINS", "http://localhost:3000,http://127.0.0.1:3000,http://localhost:3001,http://127.0.0.1:3001")
	var origins []string
	for _, o := range strings.Split(originsRaw, ",") {
		trimmed := strings.TrimSpace(o)
		if trimmed != "" {
			origins = append(origins, trimmed)
		}
	}

	cookieMode := strings.ToLower(envOr("COOKIE_SECURE", "false"))

	return Config{
		AppEnv:             envOr("APP_ENV", "development"),
		Port:               envOr("PORT", "8000"),
		CORSAllowedOrigins: origins,

		DatabaseURL: envOr("DATABASE_URL", "sqlite://./pantara_dev.db"),
		DBMaxConns:  int32(envInt("DB_MAX_CONNS", 80)),
		DBMinConns:  int32(envInt("DB_MIN_CONNS", 15)),

		RedisURL: strings.TrimSpace(os.Getenv("REDIS_URL")),

		JWTSecret:        envOr("JWT_SECRET", DefaultDevJWTSecret),
		CookieSecure:     cookieMode == "true" || cookieMode == "1" || cookieMode == "yes",
		CookieSecureAuto: cookieMode == "auto",
		CookieDomain:     strings.TrimSpace(os.Getenv("COOKIE_DOMAIN")),

		AIProviderURL: strings.TrimSpace(os.Getenv("AI_PROVIDER_URL")),
		AIAPIKey:      strings.TrimSpace(os.Getenv("AI_API_KEY")),
		AIModel:       envOr("AI_MODEL", "gpt-4o-mini"),
		AITimeout:     time.Duration(envInt("AI_TIMEOUT_SECONDS", 15)) * time.Second,

		SMTPHost:   strings.TrimSpace(os.Getenv("SMTP_HOST")),
		SMTPPort:   envInt("SMTP_PORT", 587),
		SMTPUser:   strings.TrimSpace(os.Getenv("SMTP_USER")),
		SMTPPass:   strings.TrimSpace(os.Getenv("SMTP_PASS")),
		SMTPFrom:   envOr("SMTP_FROM", "noreply@pantara.id"),
		AppBaseURL: strings.TrimSpace(envOr("APP_BASE_URL", "http://localhost:3000")),
	}
}

// IsPostgres reports whether DatabaseURL targets PostgreSQL.
func (c Config) IsPostgres() bool {
	lower := strings.ToLower(strings.TrimSpace(c.DatabaseURL))
	return strings.HasPrefix(lower, "postgres://") || strings.HasPrefix(lower, "postgresql://")
}

// SQLiteDSN strips optional sqlite:// prefix and returns the underlying DSN.
func (c Config) SQLiteDSN() string {
	dsn := strings.TrimSpace(c.DatabaseURL)
	if strings.HasPrefix(strings.ToLower(dsn), "sqlite://") {
		return dsn[len("sqlite://"):]
	}
	if dsn == "" {
		return "./pantara_dev.db"
	}
	return dsn
}

func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		val = strings.Trim(val, `"'`)
		if key != "" && os.Getenv(key) == "" {
			_ = os.Setenv(key, val)
		}
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
