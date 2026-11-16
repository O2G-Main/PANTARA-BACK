package httpadapter

import (
	"net/http"

	"github.com/Orang2-BU/pantara-back/internal/application/usecase"
	"github.com/Orang2-BU/pantara-back/internal/domain/task"
	"github.com/Orang2-BU/pantara-back/internal/domain/workspace"
	"github.com/go-chi/chi/v5"
)

type taskHandlers struct {
	uc *usecase.TaskUseCase
}

func (h *taskHandlers) list(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	periodID := r.URL.Query().Get("periodId")
	tasks, err := h.uc.ListTasks(r.Context(), claims.OrganizationID, periodID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"tasks": tasks})
}

func (h *taskHandlers) get(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	t, decs, err := h.uc.GetTask(r.Context(), claims, id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"task": t, "decisions": decs})
}

func (h *taskHandlers) create(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	var body usecase.CreateTaskInput
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	t, err := h.uc.CreateTask(r.Context(), claims, body)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeCreated(w, map[string]any{"task": t})
}

func (h *taskHandlers) update(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	var body struct {
		ProjectID   *string        `json:"projectId"`
		Title       *string        `json:"title"`
		Description *string        `json:"description"`
		DueDate     *string        `json:"dueDate"`
		Priority    *task.Priority `json:"priority"`
		Status      *task.Status   `json:"status"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	t, err := h.uc.UpdateTaskMetadata(r.Context(), claims, id, body.ProjectID, body.Title, body.Description, body.DueDate, body.Priority, body.Status)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"task": t})
}

func (h *taskHandlers) deleteOrArchive(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	if err := h.uc.DeleteOrArchiveTask(r.Context(), claims, id); err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]bool{"ok": true})
}

func (h *taskHandlers) flowStart(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	if err := h.uc.MarkFlowStart(r.Context(), claims.OrganizationID, id); err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]bool{"ok": true})
}

func (h *taskHandlers) analyzeDNA(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	t, err := h.uc.AnalyzeDNA(r.Context(), claims, id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"task": t})
}

func (h *taskHandlers) confirmDNA(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	var body task.TaskDNA
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	t, err := h.uc.ConfirmDNA(r.Context(), claims, id, body)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"task": t})
}

func (h *taskHandlers) candidates(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	preset := workspace.PresetID(r.URL.Query().Get("preset"))
	t, ranked, weights, overrun, err := h.uc.RankCandidates(r.Context(), claims.OrganizationID, id, preset)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{
		"task":       t,
		"candidates": ranked.Candidates,
		"filtered":   ranked.Filtered,
		"weights":    weights,
		"overrun":    overrun,
	})
}

func (h *taskHandlers) simulate(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	var body struct {
		Assignments []task.Assignment `json:"assignments"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	sim, err := h.uc.Simulate(r.Context(), claims.OrganizationID, id, body.Assignments)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"simulation": sim})
}

func (h *taskHandlers) assign(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	var body usecase.AssignInput
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	t, dec, err := h.uc.Assign(r.Context(), claims, id, body)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"task": t, "decision": dec})
}

func (h *taskHandlers) reject(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	var body struct {
		Preset     workspace.PresetID `json:"preset"`
		ReasonCode string             `json:"reasonCode"`
		Note       string             `json:"note"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	t, dec, err := h.uc.RejectRecommendation(r.Context(), claims, id, body.Preset, body.ReasonCode, body.Note)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"task": t, "decision": dec})
}

func (h *taskHandlers) unassign(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	t, err := h.uc.Unassign(r.Context(), claims, id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"task": t})
}

func (h *taskHandlers) updateStatus(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	var body struct {
		Status      task.Status `json:"status"`
		ActualHours *float64    `json:"actualHours"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	t, err := h.uc.UpdateStatus(r.Context(), claims, id, body.Status, body.ActualHours)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"task": t})
}

func (h *taskHandlers) batchPreview(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	preset := workspace.PresetID(r.URL.Query().Get("preset"))
	pairs, err := h.uc.BatchPreview(r.Context(), claims.OrganizationID, preset)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"pairs": pairs})
}

func (h *taskHandlers) batchCommit(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	var body struct {
		Preset workspace.PresetID        `json:"preset"`
		Items  []usecase.BatchCommitItem `json:"items"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	updated, err := h.uc.BatchCommit(r.Context(), claims, body.Preset, body.Items)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"tasks": updated})
}
