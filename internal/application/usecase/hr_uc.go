package usecase

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/Orang2-BU/pantara-back/internal/application/port"
	"github.com/Orang2-BU/pantara-back/internal/domain/auth"
	"github.com/Orang2-BU/pantara-back/internal/domain/engine"
	"github.com/Orang2-BU/pantara-back/internal/domain/errs"
	"github.com/Orang2-BU/pantara-back/internal/domain/member"
	"github.com/Orang2-BU/pantara-back/internal/domain/notification"
	"github.com/Orang2-BU/pantara-back/internal/domain/workspace"
)

var monthPeriodRe = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

// LeaveRequestView enriches a member leave request with member identity for HR inbox tables.
type LeaveRequestView struct {
	member.LeaveRequest
	MemberName     string                `json:"memberName"`
	MemberDivision string                `json:"memberDivision,omitempty"`
	MemberRole     string                `json:"memberRole,omitempty"`
	Status         member.ApprovalStatus `json:"status"`
}

// CreatePerformanceReviewCriterionInput supplies a criterion score from a manager evaluation.
type CreatePerformanceReviewCriterionInput struct {
	ID    string  `json:"id"`
	Name  string  `json:"name,omitempty"`
	Score float64 `json:"score"`
}

// CreatePerformanceReviewInput captures inputs for creating an immutable monthly performance review.
type CreatePerformanceReviewInput struct {
	MemberID string                                  `json:"memberId"`
	Period   string                                  `json:"period"`
	Note     string                                  `json:"note"`
	Criteria []CreatePerformanceReviewCriterionInput `json:"criteria"`
}

// HRUseCase orchestrates HR divisions, member placement, HR policy settings, HR inbox requests,
// parallel HR leave decisions, and performance configuration/reviews.
type HRUseCase struct {
	tx      port.TxManager
	ws      port.WorkspaceRepository
	members port.MemberRepository
	tasks   port.TaskRepository
	auth    port.AuthRepository
	notifs  port.NotificationRepository
	hr      port.HRRepository
	clock   port.Clock
}

// NewHRUseCase constructs an HRUseCase with its persistence and messaging ports.
func NewHRUseCase(
	tx port.TxManager,
	ws port.WorkspaceRepository,
	members port.MemberRepository,
	tasks port.TaskRepository,
	authRepo port.AuthRepository,
	notifs port.NotificationRepository,
	hr port.HRRepository,
	clock port.Clock,
) *HRUseCase {
	return &HRUseCase{
		tx:      tx,
		ws:      ws,
		members: members,
		tasks:   tasks,
		auth:    authRepo,
		notifs:  notifs,
		hr:      hr,
		clock:   clock,
	}
}

// --- Organization Divisions & Placement ---

// ListDivisions returns all known division names sorted alphabetically.
func (uc *HRUseCase) ListDivisions(ctx context.Context, orgID string) ([]string, error) {
	return uc.hr.ListDivisions(ctx, orgID)
}

// CreateDivision registers a new organization division if it does not already exist.
func (uc *HRUseCase) CreateDivision(ctx context.Context, orgID, name string) (string, error) {
	name = strings.TrimSpace(name)
	if len([]rune(name)) < 2 {
		return "", errs.Validation("Nama divisi minimal 2 karakter.")
	}
	existing, err := uc.hr.ListDivisions(ctx, orgID)
	if err != nil {
		return "", err
	}
	for _, d := range existing {
		if strings.EqualFold(d, name) {
			return "", errs.Conflict("Divisi ini sudah ada.")
		}
	}
	if err := uc.hr.CreateDivision(ctx, orgID, name); err != nil {
		return "", err
	}
	return name, nil
}

// RenameDivision renames an existing division and synchronizes member division assignments.
func (uc *HRUseCase) RenameDivision(ctx context.Context, orgID, oldName, newName string) (string, error) {
	oldName = strings.TrimSpace(oldName)
	newName = strings.TrimSpace(newName)
	if oldName == "" {
		return "", errs.Validation("Nama divisi asal wajib diisi.")
	}
	if len([]rune(newName)) < 2 {
		return "", errs.Validation("Nama divisi baru minimal 2 karakter.")
	}
	existing, err := uc.hr.ListDivisions(ctx, orgID)
	if err != nil {
		return "", err
	}
	foundOld := false
	for _, d := range existing {
		if strings.EqualFold(d, oldName) {
			foundOld = true
		} else if strings.EqualFold(d, newName) {
			return "", errs.Conflict("Nama divisi baru sudah digunakan.")
		}
	}
	if !foundOld {
		return "", errs.NotFound("Divisi tidak ditemukan.")
	}
	if err := uc.hr.RenameDivision(ctx, orgID, oldName, newName); err != nil {
		return "", err
	}
	return newName, nil
}

// PlaceMembers assigns one or more organization members to a division and optional team.
func (uc *HRUseCase) PlaceMembers(ctx context.Context, claims auth.Claims, memberIDs []string, division string, team *string) ([]member.Member, error) {
	division = strings.TrimSpace(division)
	if division == "" {
		return nil, errs.Validation("Divisi tujuan wajib dipilih.")
	}
	if len(memberIDs) == 0 {
		return nil, errs.Validation("Pilih minimal satu anggota.")
	}

	seen := make(map[string]struct{}, len(memberIDs))
	cleanIDs := make([]string, 0, len(memberIDs))
	for _, raw := range memberIDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		cleanIDs = append(cleanIDs, id)
	}
	if len(cleanIDs) == 0 {
		return nil, errs.Validation("Pilih minimal satu anggota.")
	}

	var cleanTeam *string
	if team != nil {
		trimmed := strings.TrimSpace(*team)
		cleanTeam = &trimmed
	}

	p, err := uc.ws.GetActivePeriod(ctx, claims.OrganizationID)
	if err != nil {
		return nil, err
	}
	all, err := uc.members.List(ctx, claims.OrganizationID, p.ID, true)
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(all))
	for _, m := range all {
		known[m.ID] = true
	}
	for _, id := range cleanIDs {
		if !known[id] {
			return nil, errs.NotFound("Anggota tidak ditemukan: " + id)
		}
	}

	if err := uc.hr.PlaceMembers(ctx, claims.OrganizationID, cleanIDs, division, cleanTeam); err != nil {
		return nil, err
	}
	return uc.members.List(ctx, claims.OrganizationID, p.ID, true)
}

// --- HR Policy & Performance Settings ---

// GetHRSettings returns organization-wide weekly and reserve hour policies.
func (uc *HRUseCase) GetHRSettings(ctx context.Context, orgID string) (workspace.HRSettings, error) {
	return uc.hr.GetHRSettings(ctx, orgID)
}

// UpdateHRSettings validates and stores weekly work hours and reserve hours (0 <= reserve <= weekly <= 168).
func (uc *HRUseCase) UpdateHRSettings(ctx context.Context, orgID string, s workspace.HRSettings) (workspace.HRSettings, error) {
	if math.IsNaN(s.WeeklyHours) || math.IsInf(s.WeeklyHours, 0) ||
		math.IsNaN(s.ReserveHours) || math.IsInf(s.ReserveHours, 0) {
		return workspace.HRSettings{}, errs.Validation("Jam kerja dan cadangan harus berupa angka valid.")
	}
	if s.WeeklyHours < 0 || s.WeeklyHours > 168 {
		return workspace.HRSettings{}, errs.Validation("Jam kerja standar mingguan harus antara 0 dan 168 jam.")
	}
	if s.ReserveHours < 0 || s.ReserveHours > s.WeeklyHours {
		return workspace.HRSettings{}, errs.Validation("Jam cadangan harus antara 0 dan jam kerja standar.")
	}
	if err := uc.hr.SaveHRSettings(ctx, orgID, s); err != nil {
		return workspace.HRSettings{}, err
	}
	return s, nil
}

// GetPerformanceConfig returns the organization's evaluation weights and criteria.
func (uc *HRUseCase) GetPerformanceConfig(ctx context.Context, orgID string) (workspace.PerformanceConfig, error) {
	return uc.hr.GetPerformanceConfig(ctx, orgID)
}

// UpdatePerformanceConfig validates and stores evaluation weights and review criteria.
func (uc *HRUseCase) UpdatePerformanceConfig(ctx context.Context, orgID string, cfg workspace.PerformanceConfig) (workspace.PerformanceConfig, error) {
	if math.IsNaN(cfg.AutomaticWeight) || math.IsInf(cfg.AutomaticWeight, 0) || cfg.AutomaticWeight < 0 || cfg.AutomaticWeight > 100 {
		return workspace.PerformanceConfig{}, errs.Validation("Bobot otomatis harus antara 0 dan 100.")
	}
	if cfg.Criteria == nil {
		cfg.Criteria = []workspace.PerformanceCriterion{}
	}
	seenIDs := make(map[string]bool, len(cfg.Criteria))
	activeCount := 0
	hasPositiveWeight := false
	for i := range cfg.Criteria {
		c := &cfg.Criteria[i]
		c.Name = strings.TrimSpace(c.Name)
		if c.Name == "" {
			return workspace.PerformanceConfig{}, errs.Validation("Nama kriteria penilaian wajib diisi.")
		}
		if math.IsNaN(c.Weight) || math.IsInf(c.Weight, 0) || c.Weight < 0 || c.Weight > 100 {
			return workspace.PerformanceConfig{}, errs.Validation("Bobot setiap kriteria harus antara 0 dan 100.")
		}
		id := strings.TrimSpace(c.ID)
		if id == "" {
			id = slugify(c.Name)
		}
		if seenIDs[id] {
			return workspace.PerformanceConfig{}, errs.Validation("ID kriteria tidak boleh duplikat.")
		}
		c.ID = id
		seenIDs[id] = true
		if c.Active {
			activeCount++
			if c.Weight > 0 {
				hasPositiveWeight = true
			}
		}
	}
	if activeCount == 0 || !hasPositiveWeight {
		return workspace.PerformanceConfig{}, errs.Validation("Minimal satu kriteria aktif harus memiliki bobot lebih dari 0.")
	}
	if err := uc.hr.SavePerformanceConfig(ctx, orgID, cfg); err != nil {
		return workspace.PerformanceConfig{}, err
	}
	return cfg, nil
}

// --- HR Inbox Requests ---

// CreateHRRequest submits a new complaint, policy, or staffing request to HR from a manager or employee.
func (uc *HRUseCase) CreateHRRequest(ctx context.Context, claims auth.Claims, title, description string) (workspace.HRRequest, error) {
	title = strings.TrimSpace(title)
	description = strings.TrimSpace(description)
	if len([]rune(title)) < 3 {
		return workspace.HRRequest{}, errs.Validation("Judul pengajuan minimal 3 karakter.")
	}
	if len([]rune(description)) < 5 {
		return workspace.HRRequest{}, errs.Validation("Deskripsi pengajuan minimal 5 karakter.")
	}
	sender := strings.TrimSpace(claims.Name)
	if sender == "" {
		sender = claims.Email
	}
	req := workspace.HRRequest{
		ID:             "hr-" + uuid.NewString()[:8],
		OrganizationID: claims.OrganizationID,
		Sender:         sender,
		SenderRole:     string(claims.Role),
		Title:          title,
		Description:    description,
		CreatedAt:      uc.clock.Now().UTC().Format("2006-01-02T15:04:05Z07:00"),
		Handled:        false,
		Read:           false,
	}
	if err := uc.hr.CreateHRRequest(ctx, req); err != nil {
		return workspace.HRRequest{}, err
	}
	return req, nil
}

// ListHRRequests returns organization HR requests filtered optionally by "semua", "belum", or "selesai".
func (uc *HRUseCase) ListHRRequests(ctx context.Context, orgID, statusFilter string) ([]workspace.HRRequest, error) {
	filter := strings.ToLower(strings.TrimSpace(statusFilter))
	switch filter {
	case "", "semua":
		filter = ""
	case "belum", "selesai":
	default:
		return nil, errs.Validation("Filter status harus semua, belum, atau selesai.")
	}
	return uc.hr.ListHRRequests(ctx, orgID, filter)
}

// MarkHRRequestRead marks an HR inbox item as read without resolving it.
func (uc *HRUseCase) MarkHRRequestRead(ctx context.Context, orgID, id string) (workspace.HRRequest, error) {
	req, err := uc.hr.GetHRRequest(ctx, orgID, id)
	if err != nil {
		return workspace.HRRequest{}, err
	}
	if !req.Read {
		req.Read = true
		if err := uc.hr.UpdateHRRequest(ctx, req); err != nil {
			return workspace.HRRequest{}, err
		}
	}
	return req, nil
}

// HandleHRRequest marks an HR inbox item as handled (and read).
func (uc *HRUseCase) HandleHRRequest(ctx context.Context, orgID, id string) (workspace.HRRequest, error) {
	req, err := uc.hr.GetHRRequest(ctx, orgID, id)
	if err != nil {
		return workspace.HRRequest{}, err
	}
	req.Read = true
	req.Handled = true
	if err := uc.hr.UpdateHRRequest(ctx, req); err != nil {
		return workspace.HRRequest{}, err
	}
	return req, nil
}

// --- HR Parallel Leave Approvals ---

// ListLeaveRequests aggregates member leave requests for HR review, newest first, optionally filtered by status.
func (uc *HRUseCase) ListLeaveRequests(ctx context.Context, orgID, statusFilter string) ([]LeaveRequestView, error) {
	statusFilter = strings.ToLower(strings.TrimSpace(statusFilter))
	switch statusFilter {
	case "", "semua":
		statusFilter = ""
	case "menunggu", "disetujui", "ditolak":
	default:
		return nil, errs.Validation("Filter status cuti harus menunggu, disetujui, atau ditolak.")
	}
	p, err := uc.ws.GetActivePeriod(ctx, orgID)
	if err != nil {
		return nil, err
	}
	members, err := uc.members.List(ctx, orgID, p.ID, true)
	if err != nil {
		return nil, err
	}
	out := make([]LeaveRequestView, 0)
	for _, m := range members {
		for _, lr := range m.LeaveRequests {
			if lr.MemberID == "" {
				lr.MemberID = m.ID
			}
			combined := lr.CombinedStatus()
			if statusFilter != "" && string(combined) != statusFilter {
				continue
			}
			out = append(out, LeaveRequestView{
				LeaveRequest:   lr,
				MemberName:     m.Name,
				MemberDivision: m.Division,
				MemberRole:     m.Role,
				Status:         combined,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].RequestedAt != out[j].RequestedAt {
			return out[i].RequestedAt > out[j].RequestedAt
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// DecideLeaveByHR records HR's parallel approval or rejection for a leave request ID,
// updating LeaveDates when both manager and HR have approved.
func (uc *HRUseCase) DecideLeaveByHR(ctx context.Context, claims auth.Claims, reqID string, approve bool) (member.Member, error) {
	now := uc.clock.Now().UTC()
	var updated member.Member
	var decidedReq *member.LeaveRequest
	var memberNotice *notification.Notice

	err := uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		period, err := uc.ws.GetActivePeriod(txCtx, claims.OrganizationID)
		if err != nil {
			return err
		}
		members, err := uc.members.List(txCtx, claims.OrganizationID, period.ID, true)
		if err != nil {
			return err
		}
		var owner member.Member
		found := false
		for _, m := range members {
			for _, lr := range m.LeaveRequests {
				if lr.ID == reqID {
					owner = m
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return errs.NotFound("Pengajuan cuti tidak ditemukan.")
		}

		next, req, ok := engine.DecideLeave(owner, reqID, "hr", approve)
		if !ok {
			return errs.Conflict("Pengajuan cuti sudah diputuskan atau tidak menunggu keputusan HR.")
		}
		next.UpdatedAt = now
		if err := uc.members.UpdateProfile(txCtx, claims.OrganizationID, next); err != nil {
			return err
		}
		if req.CombinedStatus() == member.ApprovalApproved {
			if err := uc.members.SaveAvailability(txCtx, claims.OrganizationID, period.ID, next.ID, next.AvailableHours, next.WeeklyLimit, next.LeaveDates); err != nil {
				return err
			}
		}
		updated = next
		decidedReq = req

		if u, uErr := uc.auth.GetUserByMemberID(txCtx, claims.OrganizationID, next.ID); uErr == nil {
			msg := "ditolak HR"
			if approve {
				if req.CombinedStatus() == member.ApprovalApproved {
					msg = "disetujui penuh (manajer & HR)"
				} else {
					msg = "disetujui HR, menunggu manajer"
				}
			}
			memberNotice = &notification.Notice{
				ID:             fmt.Sprintf("cuti-hr-%s", req.ID),
				OrganizationID: claims.OrganizationID,
				UserID:         u.ID,
				Title:          "Status cuti diperbarui: " + msg,
				Subtitle:       fmt.Sprintf("%s (%s - %s)", req.Type, req.Start, req.End),
				Href:           "/saya/cuti",
				Kind:           notification.KindOperational,
				CreatedAt:      now,
			}
		}
		return nil
	})
	if err != nil {
		return member.Member{}, err
	}
	if memberNotice != nil && decidedReq != nil {
		_ = uc.notifs.Upsert(ctx, *memberNotice)
	}
	return updated, nil
}

// --- Performance Reviews ---

// ListPerformanceReviews returns immutable performance reviews filtered optionally by YYYY-MM period or memberId.
func (uc *HRUseCase) ListPerformanceReviews(ctx context.Context, orgID, period, memberID string) ([]workspace.PerformanceReview, error) {
	period = strings.TrimSpace(period)
	memberID = strings.TrimSpace(memberID)
	if period != "" && !monthPeriodRe.MatchString(period) {
		return nil, errs.Validation("Format periode harus YYYY-MM.")
	}
	return uc.hr.ListPerformanceReviews(ctx, orgID, period, memberID)
}

// CreatePerformanceReview records an immutable monthly manager evaluation and snapshots automatic performance metrics.
// Note: reviews are strictly for reporting/HR records and never feed into task recommendations.
func (uc *HRUseCase) CreatePerformanceReview(ctx context.Context, claims auth.Claims, in CreatePerformanceReviewInput) (workspace.PerformanceReview, error) {
	in.MemberID = strings.TrimSpace(in.MemberID)
	in.Period = strings.TrimSpace(in.Period)
	in.Note = strings.TrimSpace(in.Note)
	if in.MemberID == "" {
		return workspace.PerformanceReview{}, errs.Validation("Anggota wajib dipilih.")
	}
	if !monthPeriodRe.MatchString(in.Period) {
		return workspace.PerformanceReview{}, errs.Validation("Format periode penilaian harus YYYY-MM.")
	}

	activePeriod, err := uc.ws.GetActivePeriod(ctx, claims.OrganizationID)
	if err != nil {
		return workspace.PerformanceReview{}, err
	}
	if _, err := uc.members.Get(ctx, claims.OrganizationID, activePeriod.ID, in.MemberID); err != nil {
		return workspace.PerformanceReview{}, err
	}

	existing, err := uc.hr.ListPerformanceReviews(ctx, claims.OrganizationID, in.Period, in.MemberID)
	if err != nil {
		return workspace.PerformanceReview{}, err
	}
	if len(existing) > 0 {
		return workspace.PerformanceReview{}, errs.Conflict("Penilaian untuk anggota dan periode ini sudah ada.")
	}

	cfg, err := uc.hr.GetPerformanceConfig(ctx, claims.OrganizationID)
	if err != nil {
		return workspace.PerformanceReview{}, err
	}

	scoreByKey := make(map[string]float64, len(in.Criteria))
	for _, sc := range in.Criteria {
		if math.IsNaN(sc.Score) || math.IsInf(sc.Score, 0) || sc.Score < 0 || sc.Score > 100 {
			return workspace.PerformanceReview{}, errs.Validation("Nilai kriteria harus antara 0 dan 100.")
		}
		if id := strings.TrimSpace(sc.ID); id != "" {
			scoreByKey[id] = sc.Score
		}
		if nm := strings.ToLower(strings.TrimSpace(sc.Name)); nm != "" {
			scoreByKey[nm] = sc.Score
		}
	}

	snapCriteria := make([]workspace.PerformanceReviewCriterion, 0, len(cfg.Criteria))
	for _, c := range cfg.Criteria {
		if !c.Active {
			continue
		}
		score, ok := scoreByKey[c.ID]
		if !ok {
			score, ok = scoreByKey[strings.ToLower(strings.TrimSpace(c.Name))]
		}
		if !ok {
			return workspace.PerformanceReview{}, errs.Validation("Lengkapi nilai untuk kriteria: " + c.Name)
		}
		snapCriteria = append(snapCriteria, workspace.PerformanceReviewCriterion{
			ID:     c.ID,
			Name:   c.Name,
			Weight: c.Weight,
			Score:  score,
		})
	}

	allTasks, err := uc.tasks.List(ctx, claims.OrganizationID, "", false)
	if err != nil {
		return workspace.PerformanceReview{}, err
	}
	onTime, quality, auto := engine.AutomaticPerformance(allTasks, in.MemberID, in.Period)

	author := strings.TrimSpace(claims.Name)
	if author == "" {
		author = claims.Email
	}
	rev := workspace.PerformanceReview{
		ID:              "rev-" + uuid.NewString()[:8],
		OrganizationID:  claims.OrganizationID,
		MemberID:        in.MemberID,
		Period:          in.Period,
		Note:            in.Note,
		Author:          author,
		AutomaticWeight: cfg.AutomaticWeight,
		AutomaticScore:  auto,
		OnTimeScore:     onTime,
		QualityScore:    quality,
		Criteria:        snapCriteria,
		CreatedAt:       uc.clock.Now().UTC(),
	}
	if err := uc.hr.CreatePerformanceReview(ctx, rev); err != nil {
		return workspace.PerformanceReview{}, err
	}
	return rev, nil
}
