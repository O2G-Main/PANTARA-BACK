package httpadapter

import (
	"net/http"

	"github.com/Orang2-BU/pantara-back/internal/application/usecase"
	"github.com/Orang2-BU/pantara-back/internal/domain/auth"
	"github.com/go-chi/chi/v5"
)

type authHandlers struct {
	uc      *usecase.AuthUseCase
	cookies cookieConfig
}

func (h *authHandlers) register(w http.ResponseWriter, r *http.Request) {
	var body struct {
		OrgName  string `json:"orgName"`
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	tok, err := h.uc.RegisterWorkspace(r.Context(), body.OrgName, body.Name, body.Email, body.Password, r.UserAgent())
	if err != nil {
		writeError(w, r, err)
		return
	}
	setAuthCookies(w, r, h.cookies, tok)
	writeCreated(w, AuthResponse{
		User:        toUserView(tok.User),
		AccessToken: tok.AccessToken,
		ExpiresIn:   int(usecase.AccessTokenTTL.Seconds()),
	})
}

func (h *authHandlers) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email      string `json:"email"`
		Password   string `json:"password"`
		RememberMe bool   `json:"rememberMe"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	tok, err := h.uc.Login(r.Context(), body.Email, body.Password, body.RememberMe, r.UserAgent())
	if err != nil {
		writeError(w, r, err)
		return
	}
	setAuthCookies(w, r, h.cookies, tok)
	writeOK(w, AuthResponse{
		User:        toUserView(tok.User),
		AccessToken: tok.AccessToken,
		ExpiresIn:   int(usecase.AccessTokenTTL.Seconds()),
	})
}

func (h *authHandlers) refresh(w http.ResponseWriter, r *http.Request) {
	rawRefresh := ""
	if c, err := r.Cookie(RefreshCookieName); err == nil {
		rawRefresh = c.Value
	}
	if rawRefresh == "" {
		var body struct {
			RefreshToken string `json:"refreshToken"`
		}
		if err := decodeJSON(w, r, &body); err == nil {
			rawRefresh = body.RefreshToken
		}
	}
	tok, err := h.uc.Refresh(r.Context(), rawRefresh, r.UserAgent())
	if err != nil {
		clearAuthCookies(w, r, h.cookies)
		writeError(w, r, err)
		return
	}
	setAuthCookies(w, r, h.cookies, tok)
	writeOK(w, AuthResponse{
		User:        toUserView(tok.User),
		AccessToken: tok.AccessToken,
		ExpiresIn:   int(usecase.AccessTokenTTL.Seconds()),
	})
}

func (h *authHandlers) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(RefreshCookieName); err == nil && c.Value != "" {
		_ = h.uc.Logout(r.Context(), c.Value)
	}
	clearAuthCookies(w, r, h.cookies)
	writeOK(w, map[string]bool{"ok": true})
}

func (h *authHandlers) logoutAll(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	if err := h.uc.LogoutAll(r.Context(), claims.UserID); err != nil {
		writeError(w, r, err)
		return
	}
	clearAuthCookies(w, r, h.cookies)
	writeOK(w, map[string]bool{"ok": true})
}

func (h *authHandlers) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.uc.RequestPasswordReset(r.Context(), body.Email); err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]bool{"ok": true})
}

func (h *authHandlers) resetPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.uc.ResetPassword(r.Context(), body.Token, body.Password); err != nil {
		writeError(w, r, err)
		return
	}
	clearAuthCookies(w, r, h.cookies)
	writeOK(w, map[string]bool{"ok": true})
}

func (h *authHandlers) inspectInvite(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	inv, err := h.uc.InspectInvitation(r.Context(), token)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{
		"organizationName": inv.OrganizationName,
		"email":            inv.Email,
		"name":             inv.Name,
		"roleTitle":        inv.RoleTitle,
		"accessRole":       inv.AccessRole,
		"invitedByName":    inv.InvitedByName,
		"expiresAt":        inv.ExpiresAt,
	})
}

func (h *authHandlers) acceptInvite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token    string `json:"token"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	tok, err := h.uc.AcceptInvitation(r.Context(), body.Token, body.Name, body.Password, r.UserAgent())
	if err != nil {
		writeError(w, r, err)
		return
	}
	setAuthCookies(w, r, h.cookies, tok)
	writeOK(w, AuthResponse{
		User:        toUserView(tok.User),
		AccessToken: tok.AccessToken,
		ExpiresIn:   int(usecase.AccessTokenTTL.Seconds()),
	})
}

func (h *authHandlers) me(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	user, err := h.uc.GetMe(r.Context(), claims.UserID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"user": toUserView(user)})
}

func (h *authHandlers) updateProfile(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	var body struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	user, err := h.uc.UpdateProfile(r.Context(), claims.UserID, body.Name, body.Email)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"user": toUserView(user)})
}

func (h *authHandlers) changePassword(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	var body struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.uc.ChangePassword(r.Context(), claims.UserID, body.CurrentPassword, body.NewPassword); err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]bool{"ok": true})
}

func (h *authHandlers) updateNotifyPrefs(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	var body auth.NotifyPrefs
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.uc.UpdateNotifyPrefs(r.Context(), claims.UserID, body); err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"notifyPrefs": body})
}
