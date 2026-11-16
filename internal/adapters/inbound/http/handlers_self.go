package httpadapter

import (
	"net/http"

	"github.com/Orang2-BU/pantara-back/internal/application/usecase"
	"github.com/Orang2-BU/pantara-back/internal/domain/member"
	"github.com/go-chi/chi/v5"
)

type selfHandlers struct {
	uc *usecase.SelfUseCase
}

func (h *selfHandlers) overview(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	ov, err := h.uc.GetOverview(r.Context(), claims)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, ov)
}

func (h *selfHandlers) updateProfile(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	var body struct {
		Competencies []member.MemberCompetency `json:"competencies"`
		Prefers      []string                  `json:"prefers"`
		Avoids       []string                  `json:"avoids"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	m, err := h.uc.UpdateProfile(r.Context(), claims, body.Competencies, body.Prefers, body.Avoids)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"member": m})
}

func (h *selfHandlers) updateAvailability(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	var body struct {
		AvailableHours float64  `json:"availableHours"`
		WeeklyLimit    float64  `json:"weeklyLimit"`
		LeaveDates     []string `json:"leaveDates"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	m, err := h.uc.UpdateAvailability(r.Context(), claims, body.AvailableHours, body.WeeklyLimit, body.LeaveDates)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"member": m})
}

func (h *selfHandlers) respondTask(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	var body struct {
		Accept bool   `json:"accept"`
		Reason string `json:"reason"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	t, err := h.uc.RespondAssignment(r.Context(), claims, id, body.Accept, body.Reason)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"task": t})
}

func (h *selfHandlers) updateProgress(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	var body struct {
		RemainingHours *float64 `json:"remainingHours"`
		Complete       bool     `json:"complete"`
		ActualHours    *float64 `json:"actualHours"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	t, err := h.uc.UpdateProgress(r.Context(), claims, id, body.RemainingHours, body.Complete, body.ActualHours)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"task": t})
}
