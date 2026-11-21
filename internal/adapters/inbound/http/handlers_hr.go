package httpadapter

import (
	"net/http"
	"net/url"

	"github.com/Orang2-BU/pantara-back/internal/application/usecase"
	"github.com/Orang2-BU/pantara-back/internal/domain/workspace"
	"github.com/go-chi/chi/v5"
)

type hrHandlers struct {
	uc *usecase.HRUseCase
}

func (h *hrHandlers) listDivisions(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	divs, err := h.uc.ListDivisions(r.Context(), claims.OrganizationID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"divisions": divs})
}

func (h *hrHandlers) createDivision(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	name, err := h.uc.CreateDivision(r.Context(), claims.OrganizationID, body.Name)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeCreated(w, map[string]any{"division": name})
}

func (h *hrHandlers) renameDivision(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	rawOld := chi.URLParam(r, "name")
	oldName, uErr := url.PathUnescape(rawOld)
	if uErr != nil || oldName == "" {
		oldName = rawOld
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	newName, err := h.uc.RenameDivision(r.Context(), claims.OrganizationID, oldName, body.Name)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"division": newName})
}

func (h *hrHandlers) placeMembers(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	var body struct {
		IDs      []string `json:"ids"`
		Division string   `json:"division"`
		Team     *string  `json:"team"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	members, err := h.uc.PlaceMembers(r.Context(), claims, body.IDs, body.Division, body.Team)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"members": members})
}

func (h *hrHandlers) getSettings(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	s, err := h.uc.GetHRSettings(r.Context(), claims.OrganizationID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"settings": s})
}

func (h *hrHandlers) updateSettings(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	var body workspace.HRSettings
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	s, err := h.uc.UpdateHRSettings(r.Context(), claims.OrganizationID, body)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"settings": s})
}

func (h *hrHandlers) getPerformanceConfig(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	cfg, err := h.uc.GetPerformanceConfig(r.Context(), claims.OrganizationID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"config": cfg})
}

func (h *hrHandlers) updatePerformanceConfig(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	var body workspace.PerformanceConfig
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	cfg, err := h.uc.UpdatePerformanceConfig(r.Context(), claims.OrganizationID, body)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"config": cfg})
}

func (h *hrHandlers) createRequest(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	var body struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	req, err := h.uc.CreateHRRequest(r.Context(), claims, body.Title, body.Description)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeCreated(w, map[string]any{"request": req})
}

func (h *hrHandlers) listRequests(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	status := r.URL.Query().Get("status")
	reqs, err := h.uc.ListHRRequests(r.Context(), claims.OrganizationID, status)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"requests": reqs})
}

func (h *hrHandlers) markRequestRead(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	req, err := h.uc.MarkHRRequestRead(r.Context(), claims.OrganizationID, id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"request": req})
}

func (h *hrHandlers) handleRequest(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	req, err := h.uc.HandleHRRequest(r.Context(), claims.OrganizationID, id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"request": req})
}

func (h *hrHandlers) listLeaveRequests(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	status := r.URL.Query().Get("status")
	reqs, err := h.uc.ListLeaveRequests(r.Context(), claims.OrganizationID, status)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"leaveRequests": reqs})
}

func (h *hrHandlers) decideLeave(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	reqID := chi.URLParam(r, "reqId")
	var body struct {
		Approve bool `json:"approve"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	m, err := h.uc.DecideLeaveByHR(r.Context(), claims, reqID, body.Approve)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"member": m})
}

func (h *hrHandlers) listPerformanceReviews(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	period := r.URL.Query().Get("period")
	memberID := r.URL.Query().Get("memberId")
	revs, err := h.uc.ListPerformanceReviews(r.Context(), claims.OrganizationID, period, memberID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"reviews": revs})
}

func (h *hrHandlers) createPerformanceReview(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	var body usecase.CreatePerformanceReviewInput
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	rev, err := h.uc.CreatePerformanceReview(r.Context(), claims, body)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeCreated(w, map[string]any{"review": rev})
}
