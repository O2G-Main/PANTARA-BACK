package httpadapter

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Orang2-BU/pantara-back/internal/application/usecase"
	"github.com/go-chi/chi/v5"
)

type analyticsHandlers struct {
	uc *usecase.ReportAndNotifUseCase
}

func (h *analyticsHandlers) listDecisions(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	decs, err := h.uc.ListDecisions(r.Context(), claims.OrganizationID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"decisions": decs})
}

func (h *analyticsHandlers) report(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	snap, err := h.uc.GetReport(r.Context(), claims.OrganizationID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, snap)
}

func (h *analyticsHandlers) updateBaseline(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	var body struct {
		BaselineMinutes float64 `json:"baselineMinutes"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.uc.UpdateBaseline(r.Context(), claims.OrganizationID, body.BaselineMinutes); err != nil {
		writeError(w, r, err)
		return
	}
	snap, err := h.uc.GetReport(r.Context(), claims.OrganizationID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, snap)
}

func (h *analyticsHandlers) capacity(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	periodID := r.URL.Query().Get("periodId")
	snap, err := h.uc.GetCapacity(r.Context(), claims.OrganizationID, periodID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, snap)
}

func (h *analyticsHandlers) skillMap(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	comps, members, atRisk, err := h.uc.GetSkillMap(r.Context(), claims.OrganizationID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{
		"competencies": comps,
		"members":      members,
		"atRisk":       atRisk,
	})
}

func (h *analyticsHandlers) relationGraph(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	graph, err := h.uc.GetRelationGraph(r.Context(), claims.OrganizationID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, graph)
}

func (h *analyticsHandlers) listNotifications(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	notices, err := h.uc.ListNotifications(r.Context(), claims)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"notifications": notices})
}

func (h *analyticsHandlers) markNotificationRead(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	if err := h.uc.MarkRead(r.Context(), claims, id); err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]bool{"ok": true})
}

func (h *analyticsHandlers) markAllNotificationsRead(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	if err := h.uc.MarkAllRead(r.Context(), claims); err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]bool{"ok": true})
}

func (h *analyticsHandlers) streamNotifications(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOK(w, map[string]string{"status": "polling"})
		return
	}
	claims := mustClaims(r.Context())
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	sendSnapshot := func() {
		notices, err := h.uc.ListNotifications(r.Context(), claims)
		if err != nil {
			return
		}
		b, err := json.Marshal(map[string]any{"notifications": notices})
		if err != nil {
			return
		}
		_, _ = fmt.Fprintf(w, "event: notifications\ndata: %s\n\n", b)
		flusher.Flush()
	}

	_, _ = fmt.Fprint(w, "event: ready\ndata: {\"ok\":true}\n\n")
	flusher.Flush()
	sendSnapshot()

	syncTicker := time.NewTicker(8 * time.Second)
	pingTicker := time.NewTicker(25 * time.Second)
	defer syncTicker.Stop()
	defer pingTicker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-syncTicker.C:
			sendSnapshot()
		case <-pingTicker.C:
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}
