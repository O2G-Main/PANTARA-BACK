package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/Orang2-BU/pantara-back/internal/application/port"
	"github.com/Orang2-BU/pantara-back/internal/domain/auth"
	"github.com/Orang2-BU/pantara-back/internal/domain/errs"
	"github.com/Orang2-BU/pantara-back/internal/domain/member"
	"github.com/Orang2-BU/pantara-back/internal/domain/notification"
	"github.com/Orang2-BU/pantara-back/internal/domain/task"
)

// SelfOverview aggregates employee workspace profile, assigned tasks, and previous period availability.
type SelfOverview struct {
	Member               member.Member                `json:"member"`
	Tasks                []task.Task                  `json:"tasks"`
	PreviousAvailability *member.AvailabilitySnapshot `json:"previousAvailability,omitempty"`
}

// SelfUseCase implements employee self-service operations under /saya (F10, P34-P38).
type SelfUseCase struct {
	tx      port.TxManager
	ws      port.WorkspaceRepository
	members port.MemberRepository
	tasks   port.TaskRepository
	auth    port.AuthRepository
	notifs  port.NotificationRepository
	clock   port.Clock
}

func NewSelfUseCase(
	tx port.TxManager,
	ws port.WorkspaceRepository,
	members port.MemberRepository,
	tasks port.TaskRepository,
	authRepo port.AuthRepository,
	notifs port.NotificationRepository,
	clock port.Clock,
) *SelfUseCase {
	return &SelfUseCase{
		tx:      tx,
		ws:      ws,
		members: members,
		tasks:   tasks,
		auth:    authRepo,
		notifs:  notifs,
		clock:   clock,
	}
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
	var prevAvail *member.AvailabilitySnapshot
	if prevPeriod, pErr := uc.ws.GetPreviousPeriod(ctx, claims.OrganizationID, period.ID); pErr == nil && prevPeriod != nil {
		if snap, sErr := uc.members.GetAvailability(ctx, memberID, prevPeriod.ID); sErr == nil {
			prevAvail = snap
		}
	}
	return SelfOverview{
		Member:               m,
		Tasks:                mine,
		PreviousAvailability: prevAvail,
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
	memberID, err := uc.requireMemberID(claims)
	if err != nil {
		return member.Member{}, err
	}
	if avail < 0 || avail > 60 {
		return member.Member{}, errs.Validation("Jam tersedia antara 0 dan 60.")
	}
	if limit < avail {
		return member.Member{}, errs.Validation("Batas kerja tidak boleh lebih kecil dari jam tersedia.")
	}
	period, err := uc.ws.GetActivePeriod(ctx, claims.OrganizationID)
	if err != nil {
		return member.Member{}, err
	}
	if leaveDates == nil {
		leaveDates = []string{}
	}
	if err := uc.members.SaveAvailability(ctx, claims.OrganizationID, period.ID, memberID, &avail, limit, leaveDates); err != nil {
		return member.Member{}, err
	}
	return uc.members.Get(ctx, claims.OrganizationID, period.ID, memberID)
}

// RespondAssignment allows an assigned employee to accept or reject (with required reason) a waiting task.
func (uc *SelfUseCase) RespondAssignment(ctx context.Context, claims auth.Claims, taskID string, accept bool, reason string) (task.Task, error) {
	memberID, err := uc.requireMemberID(claims)
	if err != nil {
		return task.Task{}, err
	}
	reason = strings.TrimSpace(reason)
	if !accept && len([]rune(reason)) < 3 {
		return task.Task{}, errs.Validation("Tulis alasan penolakan supaya manajer bisa menyesuaikan penugasan.")
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
