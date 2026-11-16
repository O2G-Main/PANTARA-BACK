package httpadapter

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Orang2-BU/pantara-back/internal/application/port"
	"github.com/Orang2-BU/pantara-back/internal/application/usecase"
	"github.com/Orang2-BU/pantara-back/internal/domain/auth"
	"github.com/Orang2-BU/pantara-back/internal/domain/errs"
	"github.com/go-chi/chi/v5/middleware"
)

type claimsCtxKey struct{}

func claimsFromCtx(ctx context.Context) (auth.Claims, bool) {
	c, ok := ctx.Value(claimsCtxKey{}).(auth.Claims)
	return c, ok
}

func mustClaims(ctx context.Context) auth.Claims {
	c, _ := claimsFromCtx(ctx)
	return c
}

func clientIP(r *http.Request) string {
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		if idx := strings.IndexByte(xff, ','); idx > 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return xff
	}
	if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
		return xri
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// requestLogger logs structured request telemetry without leaking cookies, tokens, or passwords.
func requestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if log == nil {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			if r.URL.Path == "/healthz" {
				return
			}
			log.Info("http_request",
				"req_id", middleware.GetReqID(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"dur_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}

// authRateLimit guards unauthenticated auth endpoints against brute-force bursts.
func authRateLimit(sec port.SecurityStore, limit int, window time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if sec != nil {
				key := "rl:auth:" + clientIP(r) + ":" + r.URL.Path
				if ok, err := sec.AllowRate(r.Context(), key, limit, window); err == nil && !ok {
					w.Header().Set("Retry-After", "60")
					writeError(w, r, errs.RateLimit("Terlalu banyak percobaan autentikasi. Tunggu sebentar."))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// apiRateLimit applies generous rate caps keyed by authenticated user first (never penalizing 1000 concurrent NAT users).
func apiRateLimit(sec port.SecurityStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if sec != nil {
				if claims, ok := claimsFromCtx(r.Context()); ok {
					key := "rl:usr:" + claims.UserID
					if allowed, err := sec.AllowRate(r.Context(), key, 2000, time.Minute); err == nil && !allowed {
						w.Header().Set("Retry-After", "60")
						writeError(w, r, errs.RateLimit("Permintaan terlalu padat. Tunggu sebentar."))
						return
					}
				} else {
					key := "rl:ip:" + clientIP(r)
					if allowed, err := sec.AllowRate(r.Context(), key, 10000, time.Minute); err == nil && !allowed {
						w.Header().Set("Retry-After", "60")
						writeError(w, r, errs.RateLimit("Permintaan terlalu padat. Tunggu sebentar."))
						return
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// authenticate validates JWT access tokens from HttpOnly cookies or Bearer headers and verifies live session revocation.
func authenticate(authUC *usecase.AuthUseCase) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok := extractAccessToken(r)
			if tok == "" {
				writeError(w, r, errs.Unauthorized("Silakan masuk terlebih dahulu."))
				return
			}
			claims, err := authUC.VerifyAccessClaims(r.Context(), tok)
			if err != nil {
				writeError(w, r, err)
				return
			}
			ctx := context.WithValue(r.Context(), claimsCtxKey{}, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func requireManager(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := claimsFromCtx(r.Context())
		if !ok {
			writeError(w, r, errs.Unauthorized("Silakan masuk terlebih dahulu."))
			return
		}
		if !claims.Role.CanManage() {
			writeError(w, r, errs.Forbidden("Halaman dan aksi ini khusus manajer atau admin."))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := claimsFromCtx(r.Context())
		if !ok {
			writeError(w, r, errs.Unauthorized("Silakan masuk terlebih dahulu."))
			return
		}
		if !claims.Role.CanAdminister() {
			writeError(w, r, errs.Forbidden("Pengaturan organisasi dan akses khusus admin."))
			return
		}
		next.ServeHTTP(w, r)
	})
}
