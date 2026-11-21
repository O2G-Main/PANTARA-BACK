package httpadapter

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Orang2-BU/pantara-back/internal/application/usecase"
	"github.com/Orang2-BU/pantara-back/internal/domain/auth"
	"github.com/Orang2-BU/pantara-back/internal/domain/errs"
	"github.com/go-chi/chi/v5/middleware"
)

const (
	AccessCookieName  = "pantara_access"
	RefreshCookieName = "pantara_refresh"
	maxJSONBodyBytes  = 1 << 20 // 1 MiB safety limit against oversized payloads
)

// ErrorBody mirrors the standardized frontend error contract.
type ErrorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// ErrorEnvelope wraps API errors consistently.
type ErrorEnvelope struct {
	Error ErrorBody `json:"error"`
}

// UserView is the safe public projection of auth.User (never exposes passwordHash).
type UserView struct {
	ID             string           `json:"id"`
	OrganizationID string           `json:"organizationId"`
	MemberID       string           `json:"memberId,omitempty"`
	Email          string           `json:"email"`
	Name           string           `json:"name"`
	Role           auth.Role        `json:"role"`
	Home           string           `json:"home"`
	IsActive       bool             `json:"isActive"`
	NotifyPrefs    auth.NotifyPrefs `json:"notifyPrefs"`
}

// AuthResponse is returned by login, register, refresh, accept-invite, and /me.
type AuthResponse struct {
	User        UserView `json:"user"`
	AccessToken string   `json:"accessToken,omitempty"`
	ExpiresIn   int      `json:"expiresIn,omitempty"`
}

func toUserView(u auth.User) UserView {
	return UserView{
		ID:             u.ID,
		OrganizationID: u.OrganizationID,
		MemberID:       u.MemberID,
		Email:          u.Email,
		Name:           u.Name,
		Role:           u.Role,
		Home:           u.Role.HomePath(),
		IsActive:       u.IsActive,
		NotifyPrefs:    u.NotifyPrefs,
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(payload)
}

func writeOK(w http.ResponseWriter, payload any) {
	writeJSON(w, http.StatusOK, payload)
}

func writeCreated(w http.ResponseWriter, payload any) {
	writeJSON(w, http.StatusCreated, payload)
}

func httpStatusFor(kind errs.Kind) int {
	switch kind {
	case errs.KindValidation:
		return http.StatusBadRequest
	case errs.KindUnauthorized:
		return http.StatusUnauthorized
	case errs.KindForbidden:
		return http.StatusForbidden
	case errs.KindNotFound:
		return http.StatusNotFound
	case errs.KindConflict:
		return http.StatusConflict
	case errs.KindUnprocessable:
		return http.StatusUnprocessableEntity
	case errs.KindLocked:
		return http.StatusLocked
	case errs.KindRateLimit:
		return http.StatusTooManyRequests
	default:
		return http.StatusInternalServerError
	}
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	if appErr, ok := errs.AsError(err); ok {
		msg := appErr.Message
		code := appErr.Code
		if code == "" {
			code = string(appErr.Kind)
		}
		if appErr.Kind == errs.KindInternal {
			msg = "Terjadi kendala pada server. Silakan coba beberapa saat lagi."
		}
		writeJSON(w, httpStatusFor(appErr.Kind), ErrorEnvelope{
			Error: ErrorBody{
				Code:    code,
				Message: msg,
				Fields:  appErr.Fields,
			},
		})
		return
	}
	_ = middleware.GetReqID(r.Context())
	writeJSON(w, http.StatusInternalServerError, ErrorEnvelope{
		Error: ErrorBody{
			Code:    "internal_error",
			Message: "Terjadi kendala pada server. Silakan coba beberapa saat lagi.",
		},
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return errs.Validation("Data permintaan tidak boleh kosong.")
		}
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return errs.Validation("Ukuran data melebihi batas maksimal 1 MB.")
		}
		return errs.Validation("Format JSON tidak valid.")
	}
	return nil
}

type cookieConfig struct {
	Secure bool
	Auto   bool
	Domain string
}

func isSecureCookie(r *http.Request, cfg cookieConfig) bool {
	if cfg.Secure {
		return true
	}
	if cfg.Auto && r != nil {
		if r.TLS != nil {
			return true
		}
		if proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); strings.EqualFold(proto, "https") {
			return true
		}
	}
	return false
}

func setAuthCookies(w http.ResponseWriter, r *http.Request, cfg cookieConfig, tokens usecase.AuthTokens) {
	accessMaxAge := int(usecase.AccessTokenTTL.Seconds())
	refreshMaxAge := int(usecase.RefreshShortTTL.Seconds())
	if tokens.RememberMe {
		refreshMaxAge = int(usecase.RefreshRememberTTL.Seconds())
	}
	secure := isSecureCookie(r, cfg)

	http.SetCookie(w, &http.Cookie{
		Name:     AccessCookieName,
		Value:    tokens.AccessToken,
		Path:     "/",
		Domain:   cfg.Domain,
		MaxAge:   accessMaxAge,
		Expires:  time.Now().UTC().Add(usecase.AccessTokenTTL),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    tokens.RefreshToken,
		Path:     "/api/v1/auth",
		Domain:   cfg.Domain,
		MaxAge:   refreshMaxAge,
		Expires:  time.Now().UTC().Add(time.Duration(refreshMaxAge) * time.Second),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearAuthCookies(w http.ResponseWriter, r *http.Request, cfg cookieConfig) {
	secure := isSecureCookie(r, cfg)
	http.SetCookie(w, &http.Cookie{
		Name:     AccessCookieName,
		Value:    "",
		Path:     "/",
		Domain:   cfg.Domain,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     RefreshCookieName,
		Value:    "",
		Path:     "/api/v1/auth",
		Domain:   cfg.Domain,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func extractAccessToken(r *http.Request) string {
	if c, err := r.Cookie(AccessCookieName); err == nil && strings.TrimSpace(c.Value) != "" {
		return strings.TrimSpace(c.Value)
	}
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(authz), "bearer ") {
		return strings.TrimSpace(authz[7:])
	}
	return ""
}
