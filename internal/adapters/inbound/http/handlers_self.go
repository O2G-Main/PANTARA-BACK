package httpadapter

import (
	"net/http"

	"github.com/Orang2-BU/pantara-back/internal/application/usecase"
	"github.com/Orang2-BU/pantara-back/internal/domain/errs"
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
	writeError(w, r, errs.Forbidden("Ketersediaan dan batas kerja mingguan kini diatur oleh HR."))
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

func (h *selfHandlers) requestExtraTime(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	var body struct {
		Hours  float64 `json:"hours"`
		Reason string  `json:"reason"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	t, err := h.uc.RequestExtraTime(r.Context(), claims, id, body.Hours, body.Reason)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"task": t})
}

func (h *selfHandlers) requestTaskChange(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	var body struct {
		Reason string `json:"reason"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	t, err := h.uc.RequestTaskChange(r.Context(), claims, id, body.Reason)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"task": t})
}

func (h *selfHandlers) createLeaveRequest(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	var body struct {
		Start  string           `json:"start"`
		End    string           `json:"end"`
		Type   member.LeaveType `json:"type"`
		Reason string           `json:"reason"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	m, err := h.uc.CreateLeaveRequest(r.Context(), claims, body.Start, body.End, body.Type, body.Reason)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeCreated(w, map[string]any{"member": m})
}
