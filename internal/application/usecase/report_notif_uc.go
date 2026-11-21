package usecase

import (
	"context"
	"fmt"
	"time"

	"github.com/Orang2-BU/pantara-back/internal/application/port"
	"github.com/Orang2-BU/pantara-back/internal/domain/auth"
	"github.com/Orang2-BU/pantara-back/internal/domain/decision"
	"github.com/Orang2-BU/pantara-back/internal/domain/engine"
	"github.com/Orang2-BU/pantara-back/internal/domain/errs"
	"github.com/Orang2-BU/pantara-back/internal/domain/member"
	"github.com/Orang2-BU/pantara-back/internal/domain/notification"
	"github.com/Orang2-BU/pantara-back/internal/domain/task"
	"github.com/Orang2-BU/pantara-back/internal/domain/workspace"
)

// ReportSnapshot packages PRD §4.1 impact metrics and baseline calibration.
type ReportSnapshot struct {
	BaselineMinutes float64               `json:"baselineMinutes"`
	Efficiency      float64               `json:"efficiency"`
	Stats           engine.DecisionStats  `json:"stats"`
	Stability       engine.StabilityStats `json:"stability"`
	Accuracy        engine.AccuracyStats  `json:"accuracy"`
	Overrun         engine.OverrunSummary `json:"overrun"`
	CompleteMembers int                   `json:"completeMembers"`
	TotalMembers    int                   `json:"totalMembers"`
}

// CapacitySnapshot packages member load rows, overrun, and 4-week load projections.
type CapacitySnapshot struct {
	Period      workspace.WorkPeriod          `json:"period"`
	Overrun     engine.OverrunSummary         `json:"overrun"`
	ReplanRisks []engine.ReplanRisk           `json:"replanRisks"`
	Weekly      map[string][]engine.WeekBucket `json:"weekly"`
}

// ReportAndNotifUseCase handles analytics views, baseline updates, and notification reconciliation.
type ReportAndNotifUseCase struct {
	ws       port.WorkspaceRepository
	comps    port.CompetencyRepository
	members  port.MemberRepository
	projects port.ProjectRepository
	tasks    port.TaskRepository
	decs     port.DecisionRepository
	notifs   port.NotificationRepository
	hr       port.HRRepository
	clock    port.Clock
}

func NewReportAndNotifUseCase(
	ws port.WorkspaceRepository,
	comps port.CompetencyRepository,
	members port.MemberRepository,
	projects port.ProjectRepository,
	tasks port.TaskRepository,
	decs port.DecisionRepository,
	notifs port.NotificationRepository,
	hr port.HRRepository,
	clock port.Clock,
) *ReportAndNotifUseCase {
	return &ReportAndNotifUseCase{
		ws:       ws,
		comps:    comps,
		members:  members,
		projects: projects,
		tasks:    tasks,
		decs:     decs,
		notifs:   notifs,
		hr:       hr,
		clock:    clock,
	}
}

func (uc *ReportAndNotifUseCase) todayAndNow() (string, string) {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		loc = time.FixedZone("WIB", 7*3600)
	}
	now := uc.clock.Now().In(loc)
	return now.Format("2006-01-02"), now.Format(time.RFC3339)
}

func (uc *ReportAndNotifUseCase) ListDecisions(ctx context.Context, orgID string) ([]decision.Decision, error) {
	return uc.decs.List(ctx, orgID)
}

func (uc *ReportAndNotifUseCase) GetReport(ctx context.Context, orgID string) (ReportSnapshot, error) {
	org, err := uc.ws.GetOrganization(ctx, orgID)
	if err != nil {
		return ReportSnapshot{}, err
	}
	period, err := uc.ws.GetActivePeriod(ctx, orgID)
	if err != nil {
		return ReportSnapshot{}, err
	}
	members, err := uc.members.List(ctx, orgID, period.ID, false)
	if err != nil {
		return ReportSnapshot{}, err
	}
	tasks, err := uc.tasks.List(ctx, orgID, period.ID, false)
	if err != nil {
		return ReportSnapshot{}, err
	}
	decisions, err := uc.decs.List(ctx, orgID)
	if err != nil {
		return ReportSnapshot{}, err
	}

	_, nowISO := uc.todayAndNow()
	stats := engine.ComputeDecisionStats(decisions)
	tbMs := org.BaselineMinutes * 60_000
	eff := 0.0
	if stats.Assigned > 0 && tbMs > 0 {
		eff = (tbMs - stats.AvgMs) / tbMs
	}
	complete := 0
	for _, m := range members {
		if m.AvailableHours != nil {
			complete++
		}
	}

	return ReportSnapshot{
		BaselineMinutes: org.BaselineMinutes,
		Efficiency:      eff,
		Stats:           stats,
		Stability:       engine.ComputeStabilityRate(decisions, nowISO),
		Accuracy:        engine.ComputeEstimateAccuracy(tasks),
		Overrun:         engine.OverrunFactor(tasks),
		CompleteMembers: complete,
		TotalMembers:    len(members),
	}, nil
}

func (uc *ReportAndNotifUseCase) UpdateBaseline(ctx context.Context, orgID string, minutes float64) error {
	if minutes <= 0 || minutes > 480 {
		return errs.Validation("Baseline manual (Tb) harus antara 1 dan 480 menit.")
	}
	return uc.ws.UpdateBaselineMinutes(ctx, orgID, minutes)
}

func (uc *ReportAndNotifUseCase) GetCapacity(ctx context.Context, orgID, periodID string) (CapacitySnapshot, error) {
	var period workspace.WorkPeriod
	var err error
	if periodID != "" {
		period, err = uc.ws.GetPeriod(ctx, orgID, periodID)
	} else {
		period, err = uc.ws.GetActivePeriod(ctx, orgID)
	}
	if err != nil {
		return CapacitySnapshot{}, err
	}
	members, err := uc.members.List(ctx, orgID, period.ID, false)
	if err != nil {
		return CapacitySnapshot{}, err
	}
	tasks, err := uc.tasks.List(ctx, orgID, period.ID, false)
	if err != nil {
		return CapacitySnapshot{}, err
	}
	today, _ := uc.todayAndNow()
	weekly := make(map[string][]engine.WeekBucket, len(members))
	for _, m := range members {
		if m.AvailableHours != nil {
			weekly[m.ID] = engine.WeeklyLoad(m, tasks, today, 4)
		}
	}
	return CapacitySnapshot{
		Period:      period,
		Overrun:     engine.OverrunFactor(tasks),
		ReplanRisks: engine.ReplanRisks(tasks, members, today),
		Weekly:      weekly,
	}, nil
}

func (uc *ReportAndNotifUseCase) GetSkillMap(ctx context.Context, orgID string) ([]member.Competency, []member.Member, []engine.AtRiskSkill, error) {
	p, err := uc.ws.GetActivePeriod(ctx, orgID)
	if err != nil {
		return nil, nil, nil, err
	}
	comps, err := uc.comps.List(ctx, orgID, false)
	if err != nil {
		return nil, nil, nil, err
	}
	members, err := uc.members.List(ctx, orgID, p.ID, false)
	if err != nil {
		return nil, nil, nil, err
	}
	return comps, members, engine.AtRiskCompetencies(comps, members), nil
}

func (uc *ReportAndNotifUseCase) GetRelationGraph(ctx context.Context, orgID string) (engine.Graph, error) {
	p, err := uc.ws.GetActivePeriod(ctx, orgID)
	if err != nil {
		return engine.Graph{}, err
	}
	comps, err := uc.comps.List(ctx, orgID, false)
	if err != nil {
		return engine.Graph{}, err
	}
	members, err := uc.members.List(ctx, orgID, p.ID, false)
	if err != nil {
		return engine.Graph{}, err
	}
	tasks, err := uc.tasks.List(ctx, orgID, p.ID, false)
	if err != nil {
		return engine.Graph{}, err
	}
	projects, err := uc.projects.List(ctx, orgID, false)
	if err != nil {
		return engine.Graph{}, err
	}
	return engine.BuildGraph(members, comps, tasks, projects, 900, 560), nil
}

// ListNotifications combines persisted event inbox records with fresh operational alerts
// (avoiding stale ghost operational alerts after tasks/availability are resolved).
func (uc *ReportAndNotifUseCase) ListNotifications(ctx context.Context, claims auth.Claims) ([]notification.Notice, error) {
	persisted, err := uc.notifs.ListByUser(ctx, claims.OrganizationID, claims.UserID)
	if err != nil {
		return nil, err
	}
	readMap := make(map[string]notification.Notice, len(persisted))
	inbox := make([]notification.Notice, 0, len(persisted))
	for _, n := range persisted {
		readMap[n.ID] = n
		if n.Kind != notification.KindOperational {
			inbox = append(inbox, n)
		}
	}

	p, err := uc.ws.GetActivePeriod(ctx, claims.OrganizationID)
	if err != nil {
		return inbox, nil
	}
	tasks, err := uc.tasks.List(ctx, claims.OrganizationID, p.ID, false)
	if err != nil {
		return inbox, nil
	}
	now := uc.clock.Now().UTC()
	today, _ := uc.todayAndNow()

	addOp := func(id, title, subtitle, href string) {
		prev, seen := readMap[id]
		inbox = append(inbox, notification.Notice{
			ID:             id,
			OrganizationID: claims.OrganizationID,
			UserID:         claims.UserID,
			Title:          title,
			Subtitle:       subtitle,
			Href:           href,
			Kind:           notification.KindOperational,
			Read:           seen && prev.Read,
			ReadAt:         prev.ReadAt,
			CreatedAt:      now,
		})
	}

	if claims.Role == auth.RoleMember && claims.MemberID != "" {
		for _, t := range tasks {
			if t.Status != task.StatusWaiting {
				continue
			}
			for _, a := range t.Assignments {
				if a.MemberID == claims.MemberID {
					addOp(
						"masuk-"+t.ID,
						"Penugasan baru menunggu jawabanmu",
						fmt.Sprintf("%s · %s jam · tenggat %s", t.Title, fmtNumberLite(t.EffortOrRemaining()), t.DueDate),
						"/saya/tugas/"+t.ID,
					)
				}
			}
		}
	}
	if claims.Role.CanManage() || claims.Role.CanAccessHR() {
		members, mErr := uc.members.List(ctx, claims.OrganizationID, p.ID, false)
		if mErr == nil {
			if claims.Role.CanManage() {
				openCount := 0
				for _, t := range tasks {
					if t.Status == task.StatusOpen {
						openCount++
					}
				}
				if openCount > 0 {
					addOp(
						fmt.Sprintf("open-%d", openCount),
						fmt.Sprintf("%d tugas belum punya penanggung jawab", openCount),
						"Mulai dari prioritas tertinggi di Tugas.",
						"/tugas?status=belum",
					)
				}
				for _, m := range members {
					if m.AvailableHours == nil {
						addOp("avail-"+m.ID, "Ketersediaan "+m.Name+" belum diisi", "Belum ikut rekomendasi sampai dilengkapi.", "/tim/"+m.ID)
					}
				}
				for _, t := range tasks {
					if t.Status == task.StatusOpen && t.ResponseReason != "" {
						addOp("tolak-"+t.ID, "Penugasan ditolak: "+t.Title, t.ResponseReason, "/tugas/"+t.ID)
					}
					if t.ExtraTime != nil && t.ExtraTime.Status == task.ExtraTimePending {
						addOp(
							"extra-"+t.ID,
							"Pengajuan tambah jam: "+t.Title,
							fmt.Sprintf("+%s jam menunggu persetujuan manajer.", fmtNumberLite(t.ExtraTime.Hours)),
							"/tugas/"+t.ID,
						)
					}
					if t.ChangeRequest != nil && t.ChangeRequest.Status == "pending" {
						addOp(
							"chg-"+t.ID,
							"Pengajuan perubahan tugas: "+t.Title,
							t.ChangeRequest.Reason,
							"/tugas/"+t.ID,
						)
					}
				}
				for _, r := range engine.ReplanRisks(tasks, members, today) {
					sub := fmt.Sprintf("%s kekurangan jam sampai tenggat.", r.Member.Name)
					if r.Kind == "cuti" {
						sub = fmt.Sprintf("%s cuti sampai tenggat.", r.Member.Name)
					}
					addOp("ulang-"+r.Task.ID+"-"+r.Member.ID, "Perlu dijadwalkan ulang: "+r.Task.Title, sub, "/tugas/"+r.Task.ID)
				}
				for _, t := range tasks {
					if t.Status == task.StatusWaiting {
						addOp("tunggu-"+t.ID, "Menunggu jawaban: "+t.Title, "Ditugaskan, belum dijawab karyawan.", "/tugas/"+t.ID)
					}
				}
				for _, m := range members {
					for _, lr := range m.LeaveRequests {
						if lr.CombinedStatus() == member.ApprovalPending && lr.Manager == member.ApprovalPending {
							addOp(
								"cuti-mgr-op-"+lr.ID,
								"Pengajuan cuti menunggu manajer: "+m.Name,
								fmt.Sprintf("%s (%s - %s)", lr.Type, lr.Start, lr.End),
								"/tim/"+m.ID,
							)
						}
					}
					if m.AvailableHours == nil {
						continue
					}
					if near := engine.ClashingDeadline(m.ID, tasks); near != "" {
						addOp("tenggat-"+m.ID, m.Name+" punya tenggat berdekatan", "Dua tugas aktif jatuh sekitar "+near+".", "/tim/"+m.ID)
					}
				}
			}

			if claims.Role.CanAccessHR() {
				if uc.hr != nil {
					if hrReqs, hrErr := uc.hr.ListHRRequests(ctx, claims.OrganizationID, "belum"); hrErr == nil {
						for _, req := range hrReqs {
							if !req.Read && !req.Handled {
								addOp(
									"hr-req-"+req.ID,
									"Pengajuan HR baru: "+req.Title,
									fmt.Sprintf("Dari %s (%s)", req.Sender, req.SenderRole),
									"/hr/inbox",
								)
							}
						}
					}
				}
				for _, m := range members {
					for _, lr := range m.LeaveRequests {
						if lr.CombinedStatus() == member.ApprovalPending && lr.HR == member.ApprovalPending {
							addOp(
								"cuti-hr-op-"+lr.ID,
								"Pengajuan cuti menunggu HR: "+m.Name,
								fmt.Sprintf("%s (%s - %s)", lr.Type, lr.Start, lr.End),
								"/hr/inbox",
							)
						}
					}
				}
			}
		}
	}

	return inbox, nil
}

func (uc *ReportAndNotifUseCase) MarkRead(ctx context.Context, claims auth.Claims, id string) error {
	now := uc.clock.Now().UTC()
	// Ensure operational alert read markers exist in DB before marking read
	_ = uc.notifs.Upsert(ctx, notification.Notice{
		ID:             id,
		OrganizationID: claims.OrganizationID,
		UserID:         claims.UserID,
		Title:          id,
		Href:           "/notifikasi",
		Kind:           notification.KindOperational,
		Read:           true,
		ReadAt:         &now,
		CreatedAt:      now,
	})
	return uc.notifs.MarkRead(ctx, claims.OrganizationID, claims.UserID, id, now)
}

func (uc *ReportAndNotifUseCase) MarkAllRead(ctx context.Context, claims auth.Claims) error {
	items, _ := uc.ListNotifications(ctx, claims)
	now := uc.clock.Now().UTC()
	for _, n := range items {
		if !n.Read {
			_ = uc.MarkRead(ctx, claims, n.ID)
		}
	}
	return uc.notifs.MarkAllRead(ctx, claims.OrganizationID, claims.UserID, now)
}

func fmtNumberLite(v float64) string {
	return fmt.Sprintf("%.1f", v)
}
