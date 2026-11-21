package usecase

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Orang2-BU/pantara-back/internal/application/port"
	"github.com/Orang2-BU/pantara-back/internal/domain/auth"
	"github.com/Orang2-BU/pantara-back/internal/domain/engine"
	"github.com/Orang2-BU/pantara-back/internal/domain/errs"
	"github.com/Orang2-BU/pantara-back/internal/domain/member"
	"github.com/Orang2-BU/pantara-back/internal/domain/notification"
	"github.com/Orang2-BU/pantara-back/internal/domain/task"
	"github.com/Orang2-BU/pantara-back/internal/domain/workspace"
)

// SelfOverview aggregates employee workspace profile and assigned tasks (including completed history).
type SelfOverview struct {
	Member member.Member `json:"member"`
	Tasks  []task.Task   `json:"tasks"`
}

// SelfUseCase implements employee self-service operations under /saya (F10, P34-P38, CF).
type SelfUseCase struct {
	tx      port.TxManager
	ws      port.WorkspaceRepository
	members port.MemberRepository
	tasks   port.TaskRepository
	auth    port.AuthRepository
	notifs  port.NotificationRepository
	hr      port.HRRepository
	clock   port.Clock
}

func NewSelfUseCase(
	tx port.TxManager,
	ws port.WorkspaceRepository,
	members port.MemberRepository,
	tasks port.TaskRepository,
	authRepo port.AuthRepository,
	notifs port.NotificationRepository,
	hr port.HRRepository,
	clock port.Clock,
) *SelfUseCase {
	return &SelfUseCase{
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

func (uc *SelfUseCase) wibLocation() *time.Location {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		return time.FixedZone("WIB", 7*3600)
	}
	return loc
}

func (uc *SelfUseCase) todayString() string {
	return uc.clock.Now().In(uc.wibLocation()).Format("2006-01-02")
}

func (uc *SelfUseCase) requireMemberID(claims auth.Claims) (string, error) {
	if claims.MemberID == "" {
		return "", errs.Forbidden("Akun ini tidak terhubung dengan profil karyawan.")
	}
	return claims.MemberID, nil
}

func (uc *SelfUseCase) GetOverview(ctx context.Context, claims auth.Claims) (SelfOverview, error) {
	memberID, err := uc.requireMemberID(claims)
	if err != nil {
		return SelfOverview{}, err
	}
	period, err := uc.ws.GetActivePeriod(ctx, claims.OrganizationID)
	if err != nil {
		return SelfOverview{}, err
	}
	m, err := uc.members.Get(ctx, claims.OrganizationID, period.ID, memberID)
	if err != nil {
		return SelfOverview{}, err
	}
	allTasks, err := uc.tasks.List(ctx, claims.OrganizationID, period.ID, false)
	if err != nil {
		return SelfOverview{}, err
	}
	mine := make([]task.Task, 0)
	for _, t := range allTasks {
		for _, a := range t.Assignments {
			if a.MemberID == memberID {
				mine = append(mine, t)
				break
			}
		}
	}
	return SelfOverview{
		Member: m,
		Tasks:  mine,
	}, nil
}

func (uc *SelfUseCase) UpdateProfile(
	ctx context.Context,
	claims auth.Claims,
	competencies []member.MemberCompetency,
	prefers, avoids []string,
) (member.Member, error) {
	memberID, err := uc.requireMemberID(claims)
	if err != nil {
		return member.Member{}, err
	}
	period, err := uc.ws.GetActivePeriod(ctx, claims.OrganizationID)
	if err != nil {
		return member.Member{}, err
	}
	m, err := uc.members.Get(ctx, claims.OrganizationID, period.ID, memberID)
	if err != nil {
		return member.Member{}, err
	}

	prevByID := make(map[string]member.MemberCompetency, len(m.Competencies))
	for _, c := range m.Competencies {
		prevByID[c.ID] = c
	}
	normalized := make([]member.MemberCompetency, 0, len(competencies))
	for _, c := range competencies {
		if c.Level < member.LevelBasic || c.Level > member.LevelExpert {
			continue
		}
		if prev, ok := prevByID[c.ID]; ok && prev.Level == c.Level && prev.Source == member.SourceVerified {
			normalized = append(normalized, prev)
			continue
		}
		c.Source = member.SourceClaim
		c.VerifiedBy = ""
		c.VerifiedAt = nil
		normalized = append(normalized, c)
	}

	if prefers == nil {
		prefers = []string{}
	}
	if avoids == nil {
		avoids = []string{}
	}
	m.Competencies = normalized
	m.Prefers = prefers
	m.Avoids = avoids
	m.UpdatedAt = uc.clock.Now().UTC()

	if err := uc.members.UpdateProfile(ctx, claims.OrganizationID, m); err != nil {
		return member.Member{}, err
	}
	return uc.members.Get(ctx, claims.OrganizationID, period.ID, memberID)
}

func (uc *SelfUseCase) UpdateAvailability(
	ctx context.Context,
	claims auth.Claims,
	avail float64,
	limit float64,
	leaveDates []string,
) (member.Member, error) {
	return member.Member{}, errs.Forbidden("Ketersediaan dan batas kerja mingguan kini diatur oleh HR.")
}

// RespondAssignment allows an assigned employee to accept, or a freelancer to reject with a reason.
func (uc *SelfUseCase) RespondAssignment(ctx context.Context, claims auth.Claims, taskID string, accept bool, reason string) (task.Task, error) {
	memberID, err := uc.requireMemberID(claims)
	if err != nil {
		return task.Task{}, err
	}
	reason = strings.TrimSpace(reason)
	if !accept {
		period, pErr := uc.ws.GetActivePeriod(ctx, claims.OrganizationID)
		if pErr != nil {
			return task.Task{}, pErr
		}
		m, mErr := uc.members.Get(ctx, claims.OrganizationID, period.ID, memberID)
		if mErr != nil {
			return task.Task{}, mErr
		}
		if m.EmploymentType != member.EmploymentFreelancer {
			return task.Task{}, errs.WithCode(errs.KindForbidden, "reject_not_allowed", "Karyawan tidak dapat menolak penugasan. Gunakan permintaan tambahan waktu.")
		}
		if len([]rune(reason)) < 10 {
			return task.Task{}, errs.Validation("Tulis alasan penolakan minimal 10 karakter supaya manajer bisa menyesuaikan penugasan.")
		}
	}

	now := uc.clock.Now().UTC()
	var updated task.Task
	var managerNotices []notification.Notice

	err = uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		t, err := uc.tasks.GetForUpdate(txCtx, claims.OrganizationID, taskID)
		if err != nil {
			return err
		}
		assigned := false
		for _, a := range t.Assignments {
			if a.MemberID == memberID {
				assigned = true
				break
			}
		}
		if !assigned {
			return errs.Forbidden("Tugas ini tidak ditugaskan kepadamu.")
		}
		if t.Status != task.StatusWaiting {
			return errs.Conflict("Penugasan ini sudah direspons sebelumnya.")
		}

		if accept {
			t.Status = task.StatusRunning
			t.ResponseReason = ""
		} else {
			t.Status = task.StatusOpen
			t.Assignments = []task.Assignment{}
			t.ResponseReason = reason
			t.RemainingHours = t.EffortOrRemaining()
		}
		t.UpdatedAt = now
		if err := uc.tasks.Update(txCtx, t); err != nil {
			return err
		}
		updated = t

		users, _ := uc.auth.ListUsersByOrg(txCtx, claims.OrganizationID)
		for _, u := range users {
			if !u.IsActive || !u.Role.CanManage() {
				continue
			}
			title := fmt.Sprintf("%s menerima penugasan: %s", claims.Name, t.Title)
			sub := "Tugas berpindah ke status berjalan."
			if !accept {
				title = fmt.Sprintf("%s menolak penugasan: %s", claims.Name, t.Title)
				sub = "Alasan: " + reason
			}
			managerNotices = append(managerNotices, notification.Notice{
				ID:             fmt.Sprintf("resp-%s-%s", t.ID, now.Format("150405")),
				OrganizationID: claims.OrganizationID,
				UserID:         u.ID,
				Title:          title,
				Subtitle:       sub,
				Href:           "/tugas/" + t.ID,
				Kind:           notification.KindTaskResponse,
				CreatedAt:      now,
			})
		}
		return nil
	})
	if err != nil {
		return task.Task{}, err
	}
	for _, n := range managerNotices {
		_ = uc.notifs.Upsert(ctx, n)
	}
	return updated, nil
}

// UpdateProgress updates remaining hours or completes an assigned running task with actual hours.
func (uc *SelfUseCase) UpdateProgress(ctx context.Context, claims auth.Claims, taskID string, remainingHours *float64, complete bool, actualHours *float64) (task.Task, error) {
	memberID, err := uc.requireMemberID(claims)
	if err != nil {
		return task.Task{}, err
	}
	now := uc.clock.Now().UTC()
	var updated task.Task

	err = uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		t, err := uc.tasks.GetForUpdate(txCtx, claims.OrganizationID, taskID)
		if err != nil {
			return err
		}
		assigned := false
		for _, a := range t.Assignments {
			if a.MemberID == memberID {
				assigned = true
				break
			}
		}
		if !assigned {
			return errs.Forbidden("Tugas ini tidak ditugaskan kepadamu.")
		}
		if t.Status != task.StatusRunning && t.Status != task.StatusWaiting {
			return errs.Validation("Status tugas saat ini tidak bisa diperbarui progresnya.")
		}
		if complete {
			if actualHours == nil || *actualHours <= 0 {
				return errs.Validation("Isi jam aktual lebih dari 0 saat menandai selesai.")
			}
			t.Status = task.StatusDone
			t.ActualHours = actualHours
			t.CompletedAt = uc.todayString()
			t.RemainingHours = 0
		} else if remainingHours != nil {
			if *remainingHours < 0 || *remainingHours > 60 {
				return errs.Validation("Sisa jam harus antara 0 dan 60.")
			}
			t.Status = task.StatusRunning
			t.RemainingHours = *remainingHours
		}
		t.UpdatedAt = now
		if err := uc.tasks.Update(txCtx, t); err != nil {
			return err
		}
		updated = t
		return nil
	})
	return updated, err
}

// RequestExtraTime lets an assignee on a running task request extra estimated hours within <=2h of deadline.
func (uc *SelfUseCase) RequestExtraTime(ctx context.Context, claims auth.Claims, taskID string, hours float64, reason string) (task.Task, error) {
	memberID, err := uc.requireMemberID(claims)
	if err != nil {
		return task.Task{}, err
	}
	reason = strings.TrimSpace(reason)
	if hours <= 0 || hours > 168 {
		return task.Task{}, errs.Validation("Isi tambahan jam lebih dari 0.")
	}
	if len([]rune(reason)) < 10 {
		return task.Task{}, errs.Validation("Tulis alasan minimal 10 karakter supaya manajer bisa menilai.")
	}

	now := uc.clock.Now().In(uc.wibLocation())
	var updated task.Task
	var managerNotices []notification.Notice

	err = uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		t, err := uc.tasks.GetForUpdate(txCtx, claims.OrganizationID, taskID)
		if err != nil {
			return err
		}
		assigned := false
		for _, a := range t.Assignments {
			if a.MemberID == memberID {
				assigned = true
				break
			}
		}
		if !assigned {
			return errs.WithCode(errs.KindForbidden, "not_assignee", "Tugas ini tidak ditugaskan kepadamu.")
		}
		if t.Status != task.StatusRunning {
			return errs.Validation("Tambahan waktu hanya dapat diminta untuk tugas yang sedang berjalan.")
		}
		if t.ExtraTime != nil && t.ExtraTime.Status == task.ExtraTimePending {
			return errs.WithCode(errs.KindConflict, "extra_time_pending", "Sudah ada permintaan tambahan waktu yang menunggu keputusan.")
		}
		dl, dlErr := engine.DeadlineAt(t.DueDate, t.DueTime)
		if dlErr == nil && dl.Sub(now) > engine.ExtraTimeWindow {
			return errs.WithCode(errs.KindUnprocessable, "too_early", "Tambahan waktu baru dapat diminta maksimal 2 jam sebelum tenggat.")
		}

		t.ExtraTime = &task.ExtraTimeRequest{
			MemberID:    memberID,
			Hours:       hours,
			Reason:      reason,
			RequestedAt: now.Format(time.RFC3339),
			Status:      task.ExtraTimePending,
		}
		t.UpdatedAt = now.UTC()
		if err := uc.tasks.Update(txCtx, t); err != nil {
			return err
		}
		updated = t

		users, _ := uc.auth.ListUsersByOrg(txCtx, claims.OrganizationID)
		for _, u := range users {
			if !u.IsActive || !u.Role.CanManage() {
				continue
			}
			managerNotices = append(managerNotices, notification.Notice{
				ID:             fmt.Sprintf("waktu-%s-%d", t.ID, now.UnixNano()),
				OrganizationID: claims.OrganizationID,
				UserID:         u.ID,
				Title:          fmt.Sprintf("%s minta tambahan %.1f jam", claims.Name, hours),
				Subtitle:       fmt.Sprintf("\"%s\" · %s", t.Title, reason),
				Href:           "/tugas/" + t.ID,
				Kind:           notification.KindTaskResponse,
				CreatedAt:      now.UTC(),
			})
		}
		return nil
	})
	if err != nil {
		return task.Task{}, err
	}
	for _, n := range managerNotices {
		_ = uc.notifs.Upsert(ctx, n)
	}
	return updated, nil
}

// RequestTaskChange allows a freelancer to submit a change request while keeping task assignment intact.
func (uc *SelfUseCase) RequestTaskChange(ctx context.Context, claims auth.Claims, taskID, reason string) (task.Task, error) {
	memberID, err := uc.requireMemberID(claims)
	if err != nil {
		return task.Task{}, err
	}
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) < 10 {
		return task.Task{}, errs.Validation("Tulis alasan minimal 10 karakter supaya manajer bisa meninjau.")
	}

	period, err := uc.ws.GetActivePeriod(ctx, claims.OrganizationID)
	if err != nil {
		return task.Task{}, err
	}
	m, err := uc.members.Get(ctx, claims.OrganizationID, period.ID, memberID)
	if err != nil {
		return task.Task{}, err
	}
	if m.EmploymentType != member.EmploymentFreelancer {
		return task.Task{}, errs.Forbidden("Permintaan perubahan tugas hanya tersedia untuk freelancer.")
	}

	now := uc.clock.Now().In(uc.wibLocation())
	var updated task.Task
	var notices []notification.Notice

	err = uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		t, err := uc.tasks.GetForUpdate(txCtx, claims.OrganizationID, taskID)
		if err != nil {
			return err
		}
		assigned := false
		for _, a := range t.Assignments {
			if a.MemberID == memberID {
				assigned = true
				break
			}
		}
		if !assigned {
			return errs.WithCode(errs.KindForbidden, "not_assignee", "Tugas ini tidak ditugaskan kepadamu.")
		}
		if t.Status != task.StatusWaiting && t.Status != task.StatusRunning {
			return errs.Validation("Permintaan perubahan hanya untuk tugas aktif.")
		}

		t.ChangeRequest = &task.ChangeRequest{
			MemberID: memberID,
			Reason:   reason,
			Status:   "pending",
		}
		t.UpdatedAt = now.UTC()
		if err := uc.tasks.Update(txCtx, t); err != nil {
			return err
		}
		updated = t

		if uc.hr != nil {
			hrReq := workspace.HRRequest{
				ID:             uuid.NewString(),
				OrganizationID: claims.OrganizationID,
				Sender:         m.Name,
				SenderRole:     "Freelancer",
				Title:          "Permintaan perubahan: " + t.Title,
				Description:    reason,
				CreatedAt:      now.Format(time.RFC3339),
				Handled:        false,
				Read:           false,
			}
			if err := uc.hr.CreateHRRequest(txCtx, hrReq); err != nil {
				return err
			}
		}

		users, _ := uc.auth.ListUsersByOrg(txCtx, claims.OrganizationID)
		for _, u := range users {
			if !u.IsActive || (!u.Role.CanManage() && !u.Role.IsHR()) {
				continue
			}
			href := "/tugas/" + t.ID
			if u.Role.IsHR() {
				href = "/hr/inbox"
			}
			notices = append(notices, notification.Notice{
				ID:             fmt.Sprintf("chg-%s-%d", t.ID, now.UnixNano()),
				OrganizationID: claims.OrganizationID,
				UserID:         u.ID,
				Title:          "Permintaan perubahan: " + t.Title,
				Subtitle:       fmt.Sprintf("%s: %s", m.Name, reason),
				Href:           href,
				Kind:           notification.KindTaskResponse,
				CreatedAt:      now.UTC(),
			})
		}
		return nil
	})
	if err != nil {
		return task.Task{}, err
	}
	for _, n := range notices {
		_ = uc.notifs.Upsert(ctx, n)
	}
	return updated, nil
}

// CreateLeaveRequest submits a leave request requiring parallel manager + HR approval.
func (uc *SelfUseCase) CreateLeaveRequest(ctx context.Context, claims auth.Claims, start, end string, leaveType member.LeaveType, reason string) (member.Member, error) {
	memberID, err := uc.requireMemberID(claims)
	if err != nil {
		return member.Member{}, err
	}
	if !leaveType.IsValid() {
		return member.Member{}, errs.Validation("Jenis cuti harus tahunan, sakit, atau izin.")
	}
	if msg := engine.ValidateLeaveRequest(start, end, reason, uc.todayString()); msg != "" {
		return member.Member{}, errs.Validation(msg)
	}

	now := uc.clock.Now().In(uc.wibLocation())
	var updatedMember member.Member
	var notices []notification.Notice

	err = uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		period, err := uc.ws.GetActivePeriod(txCtx, claims.OrganizationID)
		if err != nil {
			return err
		}
		m, err := uc.members.Get(txCtx, claims.OrganizationID, period.ID, memberID)
		if err != nil {
			return err
		}
		req := member.LeaveRequest{
			ID:          fmt.Sprintf("CUTI-%s-%d", memberID, len(m.LeaveRequests)+1),
			Start:       start,
			End:         end,
			Type:        leaveType,
			Reason:      strings.TrimSpace(reason),
			RequestedAt: now.Format(time.RFC3339),
			Manager:     member.ApprovalPending,
			HR:          member.ApprovalPending,
		}
		m.LeaveRequests = append([]member.LeaveRequest{req}, m.LeaveRequests...)
		m.UpdatedAt = now.UTC()
		if err := uc.members.UpdateProfile(txCtx, claims.OrganizationID, m); err != nil {
			return err
		}
		updatedMember = m

		users, _ := uc.auth.ListUsersByOrg(txCtx, claims.OrganizationID)
		for _, u := range users {
			if !u.IsActive || (!u.Role.CanManage() && !u.Role.IsHR()) {
				continue
			}
			href := "/kapasitas"
			if u.Role.IsHR() {
				href = "/hr/inbox"
			}
			notices = append(notices, notification.Notice{
				ID:             fmt.Sprintf("cuti-%s-%s", req.ID, u.ID),
				OrganizationID: claims.OrganizationID,
				UserID:         u.ID,
				Title:          fmt.Sprintf("Pengajuan cuti: %s", m.Name),
				Subtitle:       fmt.Sprintf("%s (%s - %s) · %s", leaveType, start, end, req.Reason),
				Href:           href,
				Kind:           notification.KindOperational,
				CreatedAt:      now.UTC(),
			})
		}
		return nil
	})
	if err != nil {
		return member.Member{}, err
	}
	for _, n := range notices {
		_ = uc.notifs.Upsert(ctx, n)
	}
	return updatedMember, nil
}
