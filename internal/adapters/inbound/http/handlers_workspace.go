package httpadapter

import (
	"net/http"

	"github.com/Orang2-BU/pantara-back/internal/application/usecase"
	"github.com/Orang2-BU/pantara-back/internal/domain/workspace"
	"github.com/go-chi/chi/v5"
)

type workspaceHandlers struct {
	uc *usecase.WorkspaceUseCase
}

func (h *workspaceHandlers) bootstrap(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	periodID := r.URL.Query().Get("periodId")
	state, err := h.uc.Bootstrap(r.Context(), claims, periodID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, state)
}

func (h *workspaceHandlers) getOrg(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	org, err := h.uc.GetOrganization(r.Context(), claims.OrganizationID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"organization": org})
}

func (h *workspaceHandlers) updateOrg(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	var body struct {
		Name         string               `json:"name"`
		Timezone     string               `json:"timezone"`
		PeriodType   workspace.PeriodType `json:"periodType"`
		HoursPerWeek float64              `json:"hoursPerWeek"`
		WorkDays     []string             `json:"workDays"`
		Holidays     []string             `json:"holidays"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	org, err := h.uc.UpdateOrganization(r.Context(), claims.OrganizationID, body.Name, body.Timezone, body.PeriodType, body.HoursPerWeek, body.WorkDays, body.Holidays)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"organization": org})
}

func (h *workspaceHandlers) completeOnboarding(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	var body usecase.OnboardingInput
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	created, err := h.uc.CompleteOnboarding(r.Context(), claims, body)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"members": created, "onboardingDone": true})
}

func (h *workspaceHandlers) listPeriods(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	periods, err := h.uc.ListPeriods(r.Context(), claims.OrganizationID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"periods": periods})
}

func (h *workspaceHandlers) createPeriod(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	var body struct {
		Label    string `json:"label"`
		Start    string `json:"start"`
		End      string `json:"end"`
		Activate *bool  `json:"activate"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	activate := true
	if body.Activate != nil {
		activate = *body.Activate
	}
	p, err := h.uc.CreatePeriod(r.Context(), claims.OrganizationID, body.Label, body.Start, body.End, activate)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeCreated(w, map[string]any{"period": p})
}

func (h *workspaceHandlers) activatePeriod(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	periodID := chi.URLParam(r, "id")
	p, err := h.uc.ActivatePeriod(r.Context(), claims.OrganizationID, periodID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"period": p})
}

func (h *workspaceHandlers) getSettings(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	settings, err := h.uc.GetSettings(r.Context(), claims.OrganizationID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"settings": settings})
}

func (h *workspaceHandlers) updateSettings(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	var body workspace.EngineSettings
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	settings, err := h.uc.UpdateSettings(r.Context(), claims.OrganizationID, body)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"settings": settings})
}

func (h *workspaceHandlers) getBilling(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	billing, err := h.uc.GetBilling(r.Context(), claims.OrganizationID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"billing": billing})
}
