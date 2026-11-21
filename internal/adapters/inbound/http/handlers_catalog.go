package httpadapter

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Orang2-BU/pantara-back/internal/application/usecase"
	"github.com/Orang2-BU/pantara-back/internal/domain/auth"
	"github.com/Orang2-BU/pantara-back/internal/domain/member"
	"github.com/go-chi/chi/v5"
)

type optFloat struct {
	Set   bool
	Value *float64
}

func (o *optFloat) UnmarshalJSON(data []byte) error {
	o.Set = true
	if strings.TrimSpace(string(data)) == "null" {
		o.Value = nil
		return nil
	}
	var v float64
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}

type catalogHandlers struct {
	uc               *usecase.CatalogUseCase
	includeDevTokens bool
}

// --- Competencies ---

func (h *catalogHandlers) listCompetencies(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	comps, err := h.uc.ListCompetencies(r.Context(), claims.OrganizationID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"competencies": comps})
}

func (h *catalogHandlers) createCompetency(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	var body struct {
		Name     string `json:"name"`
		Category string `json:"category"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	c, err := h.uc.CreateCompetency(r.Context(), claims.OrganizationID, body.Name, body.Category)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeCreated(w, map[string]any{"competency": c})
}

func (h *catalogHandlers) updateCompetency(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	var body struct {
		Name     string `json:"name"`
		Category string `json:"category"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	c, err := h.uc.UpdateCompetency(r.Context(), claims.OrganizationID, id, body.Name, body.Category)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"competency": c})
}

func (h *catalogHandlers) deleteCompetency(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	if err := h.uc.DeleteCompetency(r.Context(), claims.OrganizationID, id); err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]bool{"ok": true})
}

// --- Projects ---

func (h *catalogHandlers) listProjects(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	projects, err := h.uc.ListProjects(r.Context(), claims.OrganizationID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"projects": projects})
}

func (h *catalogHandlers) getProject(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	p, err := h.uc.GetProject(r.Context(), claims.OrganizationID, id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"project": p})
}

func (h *catalogHandlers) createProject(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	var body struct {
		Name    string `json:"name"`
		Client  string `json:"client"`
		DueDate string `json:"dueDate"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	p, err := h.uc.CreateProject(r.Context(), claims.OrganizationID, body.Name, body.Client, body.DueDate)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeCreated(w, map[string]any{"project": p})
}

func (h *catalogHandlers) updateProject(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	var body struct {
		Name    string `json:"name"`
		Client  string `json:"client"`
		DueDate string `json:"dueDate"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	p, err := h.uc.UpdateProject(r.Context(), claims.OrganizationID, id, body.Name, body.Client, body.DueDate)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"project": p})
}

func (h *catalogHandlers) deleteProject(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	if err := h.uc.DeleteProject(r.Context(), claims.OrganizationID, id); err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]bool{"ok": true})
}

// --- Members ---

func (h *catalogHandlers) listMembers(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	periodID := r.URL.Query().Get("periodId")
	includeInvites := r.URL.Query().Get("settings") == "1"
	if includeInvites && (claims.Role.CanManage() || claims.Role.CanAccessHR()) {
		members, invites, err := h.uc.ListMembersWithSettings(r.Context(), claims.OrganizationID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeOK(w, map[string]any{"members": members, "invitations": invites})
		return
	}
	members, err := h.uc.ListMembers(r.Context(), claims.OrganizationID, periodID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"members": members})
}

func (h *catalogHandlers) getMember(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	periodID := r.URL.Query().Get("periodId")
	m, err := h.uc.GetMember(r.Context(), claims.OrganizationID, periodID, id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"member": m})
}

func (h *catalogHandlers) createMember(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	var body usecase.CreateMemberInput
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	m, rawTok, err := h.uc.CreateAndInviteMember(r.Context(), claims, body)
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp := map[string]any{"member": m}
	if h.includeDevTokens {
		resp["inviteToken"] = rawTok
	}
	writeCreated(w, resp)
}

func (h *catalogHandlers) resendInvite(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	rawTok, err := h.uc.ResendInvite(r.Context(), claims, id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp := map[string]any{"ok": true}
	if h.includeDevTokens {
		resp["inviteToken"] = rawTok
	}
	writeOK(w, resp)
}

func (h *catalogHandlers) updateMember(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	var body struct {
		Name                 *string                `json:"name"`
		Role                 *string                `json:"role"`
		Email                *string                `json:"email"`
		WeeklyLimit          *float64               `json:"weeklyLimit"`
		EmploymentType       *member.EmploymentType `json:"employmentType"`
		Team                 *string                `json:"team"`
		Division             *string                `json:"division"`
		WorkHoursOverride    optFloat               `json:"workHoursOverride"`
		ReserveHoursOverride optFloat               `json:"reserveHoursOverride"`
		AvailableHours       optFloat               `json:"availableHours"`
		IsActive             *bool                  `json:"isActive"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	in := usecase.UpdateMemberInput{
		Name:                    body.Name,
		RoleTitle:               body.Role,
		Email:                   body.Email,
		WeeklyLimit:             body.WeeklyLimit,
		EmploymentType:          body.EmploymentType,
		Team:                    body.Team,
		Division:                body.Division,
		WorkHoursOverride:       body.WorkHoursOverride.Value,
		HasWorkHoursOverride:    body.WorkHoursOverride.Set,
		ReserveHoursOverride:    body.ReserveHoursOverride.Value,
		HasReserveHoursOverride: body.ReserveHoursOverride.Set,
		AvailableHours:          body.AvailableHours.Value,
		HasAvailableHours:       body.AvailableHours.Set,
		IsActive:                body.IsActive,
	}
	m, err := h.uc.UpdateMemberProfile(r.Context(), claims, id, in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"member": m})
}

func (h *catalogHandlers) importMembers(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	var body struct {
		CSV    string `json:"csv"`
		DryRun bool   `json:"dryRun"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	rows, err := h.uc.ImportMembersCSV(r.Context(), claims, body.CSV, body.DryRun)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"rows": rows})
}

func (h *catalogHandlers) decideLeaveByManager(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	memberID := chi.URLParam(r, "id")
	reqID := chi.URLParam(r, "reqId")
	var body struct {
		Approve bool `json:"approve"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	m, err := h.uc.DecideMemberLeaveByManager(r.Context(), claims, memberID, reqID, body.Approve)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"member": m})
}

func (h *catalogHandlers) updateMemberAvailability(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	var body struct {
		PeriodID       string   `json:"periodId"`
		AvailableHours *float64 `json:"availableHours"`
		WeeklyLimit    float64  `json:"weeklyLimit"`
		LeaveDates     []string `json:"leaveDates"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	m, err := h.uc.SaveMemberAvailability(r.Context(), claims.OrganizationID, body.PeriodID, id, body.AvailableHours, body.WeeklyLimit, body.LeaveDates)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"member": m})
}

func (h *catalogHandlers) verifyCompetency(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	compID := chi.URLParam(r, "compId")
	m, err := h.uc.VerifyCompetency(r.Context(), claims, id, compID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"member": m})
}

func (h *catalogHandlers) setMemberAccessAndActive(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	var body struct {
		Access   *auth.Role `json:"access"`
		IsActive *bool      `json:"isActive"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	m, err := h.uc.SetMemberAccessAndActive(r.Context(), claims, id, body.Access, body.IsActive)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]any{"member": m})
}

func (h *catalogHandlers) remindMember(w http.ResponseWriter, r *http.Request) {
	claims := mustClaims(r.Context())
	id := chi.URLParam(r, "id")
	if err := h.uc.RemindMember(r.Context(), claims, id); err != nil {
		writeError(w, r, err)
		return
	}
	writeOK(w, map[string]bool{"ok": true})
}
