package usecase

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
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

// TaskUseCase manages full task lifecycle, Task DNA, candidate ranking, simulation, assignment, and batch allocation.
type TaskUseCase struct {
	tx      port.TxManager
	ws      port.WorkspaceRepository
	comps   port.CompetencyRepository
	members port.MemberRepository
	projects port.ProjectRepository
	tasks   port.TaskRepository
	decs    port.DecisionRepository
	auth    port.AuthRepository
	notifs  port.NotificationRepository
	ai      port.TaskDNAnalyzer
	clock   port.Clock
}

func NewTaskUseCase(
	tx port.TxManager,
	ws port.WorkspaceRepository,
	comps port.CompetencyRepository,
	members port.MemberRepository,
	projects port.ProjectRepository,
	tasks port.TaskRepository,
	decs port.DecisionRepository,
	authRepo port.AuthRepository,
	notifs port.NotificationRepository,
	ai port.TaskDNAnalyzer,
	clock port.Clock,
) *TaskUseCase {
	return &TaskUseCase{
		tx:       tx,
		ws:       ws,
		comps:    comps,
		members:  members,
		projects: projects,
		tasks:    tasks,
		decs:     decs,
		auth:     authRepo,
		notifs:   notifs,
		ai:       ai,
		clock:    clock,
	}
}

func (uc *TaskUseCase) todayString() string {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		loc = time.FixedZone("WIB", 7*3600)
	}
	return uc.clock.Now().In(loc).Format("2006-01-02")
}

func (uc *TaskUseCase) ListTasks(ctx context.Context, orgID, periodID string) ([]task.Task, error) {
	if periodID == "" {
		p, err := uc.ws.GetActivePeriod(ctx, orgID)
		if err != nil {
			return nil, err
		}
		periodID = p.ID
	}
	return uc.tasks.List(ctx, orgID, periodID, false)
}

// GetTask enforces that anggota can read only tasks currently assigned to them.
func (uc *TaskUseCase) GetTask(ctx context.Context, claims auth.Claims, taskID string) (task.Task, []decision.Decision, error) {
	t, err := uc.tasks.Get(ctx, claims.OrganizationID, taskID)
	if err != nil {
		return task.Task{}, nil, err
	}
	if claims.Role == auth.RoleMember {
		assigned := false
		for _, a := range t.Assignments {
			if a.MemberID == claims.MemberID {
				assigned = true
				break
			}
		}
		if !assigned {
			return task.Task{}, nil, errs.Forbidden("Kamu tidak memiliki akses ke tugas ini.")
		}
	}
	decs, err := uc.decs.ListByTask(ctx, claims.OrganizationID, t.ID)
	if err != nil {
		return task.Task{}, nil, err
	}
	return t, decs, nil
}

var dueTimeRe = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

func normalizeDueTime(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "17:00", nil
	}
	if !dueTimeRe.MatchString(trimmed) {
		return "", errs.Validation("Jam tenggat harus berformat HH:MM (contoh 17:00).")
	}
	return trimmed, nil
}

// CreateTaskInput contains manager inputs when creating a task (P11).
type CreateTaskInput struct {
	ProjectID   string        `json:"projectId"`
	Title       string        `json:"title"`
	Description string        `json:"description"`
	DueDate     string        `json:"dueDate"`
	DueTime     string        `json:"dueTime"`
	Priority    task.Priority `json:"priority"`
	Draft       bool          `json:"draft"`
}

func (uc *TaskUseCase) CreateTask(ctx context.Context, claims auth.Claims, in CreateTaskInput) (task.Task, error) {
	title := strings.TrimSpace(in.Title)
	desc := strings.TrimSpace(in.Description)
	if title == "" {
		return task.Task{}, errs.Validation("Tulis judul tugas.")
	}
	if !in.Draft && len([]rune(desc)) < 20 {
		return task.Task{}, errs.Validation("Deskripsi minimal 20 karakter supaya kebutuhan bisa diurai.")
	}
	if in.DueDate == "" {
		return task.Task{}, errs.Validation("Pilih tenggat.")
	}
	if in.DueDate < uc.todayString() {
		return task.Task{}, errs.Validation("Tenggat sudah lewat. Pilih hari ini atau sesudahnya.")
	}
	dueTime, err := normalizeDueTime(in.DueTime)
	if err != nil {
		return task.Task{}, err
	}
	if !in.Priority.IsValid() {
		in.Priority = task.PriorityNormal
	}
	if _, err := uc.projects.Get(ctx, claims.OrganizationID, in.ProjectID); err != nil {
		return task.Task{}, errs.Validation("Proyek tidak ditemukan.")
	}

	period, err := uc.ws.GetActivePeriod(ctx, claims.OrganizationID)
	if err != nil {
		return task.Task{}, err
	}

	status := task.StatusOpen
	if in.Draft {
		status = task.StatusDraft
	}

	var dna *task.TaskDNA
	var remHours float64
	if !in.Draft {
		catalog, _ := uc.comps.List(ctx, claims.OrganizationID, false)
		existingTasks, _ := uc.tasks.List(ctx, claims.OrganizationID, period.ID, false)
		analyzed, aErr := uc.ai.Analyze(ctx, title, desc, in.Priority, catalog, existingTasks)
		if aErr == nil {
			dna = &analyzed
			remHours = analyzed.EffortHours
		}
	}

	now := uc.clock.Now().UTC()
	var created task.Task
	err = uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		code, codeErr := uc.tasks.NextTaskCode(txCtx, claims.OrganizationID)
		if codeErr != nil {
			return codeErr
		}
		created = task.Task{
			ID:             code,
			OrganizationID: claims.OrganizationID,
			ProjectID:      in.ProjectID,
			PeriodID:       period.ID,
			Title:          title,
			Description:    desc,
			DueDate:        in.DueDate,
			DueTime:        dueTime,
			Priority:       in.Priority,
			Status:         status,
			Assignments:    []task.Assignment{},
			RemainingHours: remHours,
			CreatedAt:      now.Format(time.RFC3339),
			DNA:            dna,
			CompetencyIDs:  []string{},
			FlowStartedAt:  &now,
			CreatedBy:      claims.UserID,
			UpdatedAt:      now,
		}
		return uc.tasks.Create(txCtx, created)
	})
	return created, err
}

func (uc *TaskUseCase) UpdateTaskMetadata(ctx context.Context, claims auth.Claims, taskID string, projectID, title, description, dueDate, dueTime *string, priority *task.Priority, status *task.Status) (task.Task, error) {
	t, err := uc.tasks.Get(ctx, claims.OrganizationID, taskID)
	if err != nil {
		return task.Task{}, err
	}
	if projectID != nil && *projectID != "" {
		if _, pErr := uc.projects.Get(ctx, claims.OrganizationID, *projectID); pErr != nil {
			return task.Task{}, errs.Validation("Proyek tidak ditemukan.")
		}
		t.ProjectID = *projectID
	}
	if title != nil {
		trimmed := strings.TrimSpace(*title)
		if trimmed == "" {
			return task.Task{}, errs.Validation("Judul tugas tidak boleh kosong.")
		}
		t.Title = trimmed
	}
	if description != nil {
		t.Description = strings.TrimSpace(*description)
	}
	if dueDate != nil && *dueDate != "" {
		t.DueDate = *dueDate
	}
	if dueTime != nil {
		dt, dtErr := normalizeDueTime(*dueTime)
		if dtErr != nil {
			return task.Task{}, dtErr
		}
		t.DueTime = dt
	}
	if priority != nil && priority.IsValid() {
		t.Priority = *priority
	}
	if status != nil && *status == task.StatusDraft && t.Status == task.StatusOpen {
		t.Status = task.StatusDraft
	}
	t.UpdatedAt = uc.clock.Now().UTC()
	if err := uc.tasks.Update(ctx, t); err != nil {
		return task.Task{}, err
	}
	return t, nil
}

func (uc *TaskUseCase) DeleteOrArchiveTask(ctx context.Context, claims auth.Claims, taskID string) error {
	t, err := uc.tasks.Get(ctx, claims.OrganizationID, taskID)
	if err != nil {
		return err
	}
	if t.Status == task.StatusWaiting || t.Status == task.StatusRunning {
		return errs.Conflict("Batalkan penugasan aktif terlebih dahulu sebelum mengarsipkan atau menghapus tugas.")
	}
	return uc.tasks.DeleteOrArchive(ctx, claims.OrganizationID, taskID)
}

func (uc *TaskUseCase) MarkFlowStart(ctx context.Context, orgID, taskID string) error {
	return uc.tasks.SetFlowStartedIfEmpty(ctx, orgID, taskID, uc.clock.Now().UTC())
}

func (uc *TaskUseCase) AnalyzeDNA(ctx context.Context, claims auth.Claims, taskID string) (task.Task, error) {
	t, err := uc.tasks.Get(ctx, claims.OrganizationID, taskID)
	if err != nil {
		return task.Task{}, err
	}
	catalog, _ := uc.comps.List(ctx, claims.OrganizationID, false)
	existing, _ := uc.tasks.List(ctx, claims.OrganizationID, t.PeriodID, false)
	dna, err := uc.ai.Analyze(ctx, t.Title, t.Description, t.Priority, catalog, existing)
	if err != nil {
		return task.Task{}, err
	}
	dna.Confirmed = false
	t.DNA = &dna
	t.RemainingHours = dna.EffortHours
	t.UpdatedAt = uc.clock.Now().UTC()
	if err := uc.tasks.Update(ctx, t); err != nil {
		return task.Task{}, err
	}
	return t, nil
}

func (uc *TaskUseCase) ConfirmDNA(ctx context.Context, claims auth.Claims, taskID string, dna task.TaskDNA) (task.Task, error) {
	if len(dna.Requirements) == 0 {
		return task.Task{}, errs.Validation("Tambahkan minimal satu kompetensi yang dibutuhkan.")
	}
	if dna.EffortHours <= 0 || dna.EffortHours > 60 {
		return task.Task{}, errs.Validation("Estimasi jam kerja harus antara 1 dan 60 jam.")
	}
	if !dna.EffortSize.IsValid() {
		dna.EffortSize = task.EffortM
	}
	t, err := uc.tasks.Get(ctx, claims.OrganizationID, taskID)
	if err != nil {
		return task.Task{}, err
	}
	now := uc.clock.Now().UTC()
	dna.Confirmed = true
	dna.ConfirmedAt = &now
	t.DNA = &dna
	t.RemainingHours = dna.EffortHours
	compIDs := make([]string, 0, len(dna.Requirements))
	for _, r := range dna.Requirements {
		compIDs = append(compIDs, r.CompetencyID)
	}
	t.CompetencyIDs = compIDs
	if t.Status == task.StatusDraft {
		t.Status = task.StatusOpen
	}
	t.UpdatedAt = now
	if err := uc.tasks.Update(ctx, t); err != nil {
		return task.Task{}, err
	}
	return t, nil
}

func (uc *TaskUseCase) loadEngineContext(ctx context.Context, orgID, periodID string) (
	members []member.Member,
	tasks []task.Task,
	settings workspace.EngineSettings,
	names map[string]string,
	err error,
) {
	if periodID == "" {
		p, pErr := uc.ws.GetActivePeriod(ctx, orgID)
		if pErr != nil {
			return nil, nil, settings, nil, pErr
		}
		periodID = p.ID
	}
	members, err = uc.members.List(ctx, orgID, periodID, false)
	if err != nil {
		return
	}
	tasks, err = uc.tasks.List(ctx, orgID, periodID, false)
	if err != nil {
		return
	}
	settings, err = uc.ws.GetEngineSettings(ctx, orgID)
	if err != nil {
		return
	}
	comps, err := uc.comps.List(ctx, orgID, false)
	if err != nil {
		return
	}
	names = make(map[string]string, len(comps))
	for _, c := range comps {
		names[c.ID] = c.Name
	}
	return
}

func (uc *TaskUseCase) RankCandidates(ctx context.Context, orgID, taskID string, presetOverride workspace.PresetID) (task.Task, engine.RankResult, engine.Weights, engine.OverrunSummary, error) {
	t, err := uc.tasks.Get(ctx, orgID, taskID)
	if err != nil {
		return task.Task{}, engine.RankResult{}, engine.Weights{}, engine.OverrunSummary{}, err
	}
	if t.DNA == nil || !t.DNA.Confirmed {
		return task.Task{}, engine.RankResult{}, engine.Weights{}, engine.OverrunSummary{}, errs.Validation("Kebutuhan tugas belum dikonfirmasi.")
	}
	members, tasks, settings, names, err := uc.loadEngineContext(ctx, orgID, t.PeriodID)
	if err != nil {
		return task.Task{}, engine.RankResult{}, engine.Weights{}, engine.OverrunSummary{}, err
	}
	if presetOverride.IsValid() {
		settings.Preset = presetOverride
	}
	res := engine.RankCandidates(t, members, tasks, settings, names, uc.todayString())
	w := engine.EffectiveWeights(settings.Preset, t.DNA.Complexity)
	overrun := engine.OverrunFactor(tasks)
	return t, res, w, overrun, nil
}

func (uc *TaskUseCase) Simulate(ctx context.Context, orgID, taskID string, scenario []task.Assignment) (engine.SimulationResult, error) {
	t, err := uc.tasks.Get(ctx, orgID, taskID)
	if err != nil {
		return engine.SimulationResult{}, err
	}
	if len(scenario) == 0 {
		return engine.SimulationResult{}, errs.Validation("Pilih minimal satu kandidat untuk disimulasikan.")
	}
	members, tasks, settings, _, err := uc.loadEngineContext(ctx, orgID, t.PeriodID)
	if err != nil {
		return engine.SimulationResult{}, err
	}
	return engine.Simulate(t, scenario, members, tasks, settings.OverloadAt), nil
}

// AssignInput carries a manager's confirmed assignment decision.
type AssignInput struct {
	Assignments []task.Assignment  `json:"assignments"`
	Preset      workspace.PresetID `json:"preset"`
	ReasonCode  string             `json:"reasonCode"`
	Note        string             `json:"note"`
}

func (uc *TaskUseCase) Assign(ctx context.Context, claims auth.Claims, taskID string, in AssignInput) (task.Task, decision.Decision, error) {
	if len(in.Assignments) == 0 {
		return task.Task{}, decision.Decision{}, errs.Validation("Pilih minimal satu penanggung jawab.")
	}
	for _, a := range in.Assignments {
		if a.Hours <= 0 {
			return task.Task{}, decision.Decision{}, errs.Validation("Jam alokasi harus lebih dari 0.")
		}
	}

	now := uc.clock.Now().UTC()
	var updatedTask task.Task
	var dec decision.Decision
	var memberNotifs []notification.Notice

	err := uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		t, err := uc.tasks.GetForUpdate(txCtx, claims.OrganizationID, taskID)
		if err != nil {
			return err
		}
		if t.Status != task.StatusOpen {
			return errs.Conflict("Tugas ini sudah ditugaskan oleh permintaan lain.")
		}
		if t.DNA == nil || !t.DNA.Confirmed {
			return errs.Validation("Konfirmasi Analisis kebutuhan terlebih dahulu sebelum menetapkan penugasan.")
		}

		members, tasks, settings, names, err := uc.loadEngineContext(txCtx, claims.OrganizationID, t.PeriodID)
		if err != nil {
			return err
		}
		preset := settings.Preset
		if in.Preset.IsValid() {
			preset = in.Preset
		}
		settings.Preset = preset

		ranked := engine.RankCandidates(t, members, tasks, settings, names, uc.todayString())
		topLimit := 3
		if len(ranked.Candidates) < topLimit {
			topLimit = len(ranked.Candidates)
		}
		var rank *int
		isSplit := len(in.Assignments) > 1
		if !isSplit {
			for i := 0; i < topLimit; i++ {
				if ranked.Candidates[i].Member.ID == in.Assignments[0].MemberID {
					r := i + 1
					rank = &r
					break
				}
			}
		} else {
			r := 1
			rank = &r
		}

		if rank == nil && !isSplit {
			if _, valid := decision.OverrideReasonLabels[in.ReasonCode]; !valid {
				return errs.Validation("Pilih alasan memilih di luar tiga teratas.")
			}
			if in.ReasonCode == "lainnya" && strings.TrimSpace(in.Note) == "" {
				return errs.Validation("Tulis catatan alasan override.")
			}
		}

		sim := engine.Simulate(t, in.Assignments, members, tasks, settings.OverloadAt)
		decisionMs := int64(180_000)
		if t.FlowStartedAt != nil {
			ms := now.Sub(*t.FlowStartedAt).Milliseconds()
			if ms > 0 {
				decisionMs = ms
			}
		}

		kptCode, err := uc.decs.NextDecisionCode(txCtx, claims.OrganizationID)
		if err != nil {
			return err
		}

		mids := make([]string, len(in.Assignments))
		for i, a := range in.Assignments {
			mids[i] = a.MemberID
		}
		dec = decision.Decision{
			ID:             kptCode,
			OrganizationID: claims.OrganizationID,
			TaskID:         t.ID,
			DecidedBy:      claims.UserID,
			MemberIDs:      mids,
			Rank:           rank,
			Preset:         preset,
			ReasonCode:     in.ReasonCode,
			Note:           strings.TrimSpace(in.Note),
			DecidedAt:      now.Format(time.RFC3339),
			DecisionMs:     decisionMs,
			ConflictsSeen:  len(sim.Conflicts),
		}

		t.Status = task.StatusWaiting
		t.Assignments = in.Assignments
		t.ResponseReason = ""
		t.RemainingHours = t.EffortOrRemaining()
		t.UpdatedAt = now
		if err := uc.tasks.Update(txCtx, t); err != nil {
			return err
		}
		if err := uc.decs.Create(txCtx, dec); err != nil {
			return err
		}
		updatedTask = t

		for _, a := range in.Assignments {
			if u, uErr := uc.auth.GetUserByMemberID(txCtx, claims.OrganizationID, a.MemberID); uErr == nil {
				memberNotifs = append(memberNotifs, notification.Notice{
					ID:             fmt.Sprintf("assign-%s-%s", t.ID, a.MemberID),
					OrganizationID: claims.OrganizationID,
					UserID:         u.ID,
					Title:          "Penugasan baru: " + t.Title,
					Subtitle:       fmt.Sprintf("%s menugaskan %.1f jam. Terima atau tolak dengan alasan.", claims.Name, a.Hours),
					Href:           "/saya/tugas/" + t.ID,
					Kind:           notification.KindTaskAssigned,
					CreatedAt:      now,
				})
			}
		}
		return nil
	})
	if err != nil {
		return task.Task{}, decision.Decision{}, err
	}
	for _, n := range memberNotifs {
		_ = uc.notifs.Upsert(ctx, n)
	}
	return updatedTask, dec, nil
}

func (uc *TaskUseCase) RejectRecommendation(ctx context.Context, claims auth.Claims, taskID string, preset workspace.PresetID, reasonCode, note string) (task.Task, decision.Decision, error) {
	if _, valid := decision.RejectReasonLabels[reasonCode]; !valid {
		return task.Task{}, decision.Decision{}, errs.Validation("Pilih alasan menolak rekomendasi.")
	}
	if reasonCode == "lainnya" && strings.TrimSpace(note) == "" {
		return task.Task{}, decision.Decision{}, errs.Validation("Tulis alasannya.")
	}
	now := uc.clock.Now().UTC()
	var updated task.Task
	var dec decision.Decision

	err := uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		t, err := uc.tasks.GetForUpdate(txCtx, claims.OrganizationID, taskID)
		if err != nil {
			return err
		}
		settings, err := uc.ws.GetEngineSettings(txCtx, claims.OrganizationID)
		if err != nil {
			return err
		}
		if !preset.IsValid() {
			preset = settings.Preset
		}
		decisionMs := int64(180_000)
		if t.FlowStartedAt != nil {
			ms := now.Sub(*t.FlowStartedAt).Milliseconds()
			if ms > 0 {
				decisionMs = ms
			}
		}
		code, err := uc.decs.NextDecisionCode(txCtx, claims.OrganizationID)
		if err != nil {
			return err
		}
		dec = decision.Decision{
			ID:             code,
			OrganizationID: claims.OrganizationID,
			TaskID:         t.ID,
			DecidedBy:      claims.UserID,
			MemberIDs:      []string{},
			Rank:           nil,
			Rejected:       true,
			Preset:         preset,
			ReasonCode:     reasonCode,
			Note:           strings.TrimSpace(note),
			DecidedAt:      now.Format(time.RFC3339),
			DecisionMs:     decisionMs,
			ConflictsSeen:  0,
		}
		if err := uc.decs.Create(txCtx, dec); err != nil {
			return err
		}
		if reasonCode == "tunda" {
			t.Status = task.StatusDraft
			t.UpdatedAt = now
			if err := uc.tasks.Update(txCtx, t); err != nil {
				return err
			}
		}
		updated = t
		return nil
	})
	return updated, dec, err
}

func (uc *TaskUseCase) Unassign(ctx context.Context, claims auth.Claims, taskID string) (task.Task, error) {
	var updated task.Task
	err := uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		t, err := uc.tasks.GetForUpdate(txCtx, claims.OrganizationID, taskID)
		if err != nil {
			return err
		}
		if t.Status != task.StatusWaiting && t.Status != task.StatusRunning {
			return errs.Validation("Hanya tugas menunggu atau berjalan yang bisa dibatalkan penugasannya.")
		}
		t.Status = task.StatusOpen
		t.Assignments = []task.Assignment{}
		t.RemainingHours = t.EffortOrRemaining()
		t.UpdatedAt = uc.clock.Now().UTC()
		if err := uc.tasks.Update(txCtx, t); err != nil {
			return err
		}
		updated = t
		return nil
	})
	return updated, err
}

func (uc *TaskUseCase) UpdateStatus(ctx context.Context, claims auth.Claims, taskID string, status task.Status, actualHours, qualityScore *float64) (task.Task, error) {
	if !status.IsValid() {
		return task.Task{}, errs.Validation("Status tidak dikenal.")
	}
	if qualityScore != nil && (*qualityScore < 0 || *qualityScore > 100) {
		return task.Task{}, errs.Validation("Nilai kualitas hasil harus antara 0 dan 100.")
	}
	var updated task.Task
	err := uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		t, err := uc.tasks.GetForUpdate(txCtx, claims.OrganizationID, taskID)
		if err != nil {
			return err
		}
		if status == task.StatusDone {
			if actualHours == nil || *actualHours <= 0 {
				return errs.Validation("Isi perkiraan jam aktual lebih dari 0.")
			}
			t.ActualHours = actualHours
			t.QualityScore = qualityScore
			t.CompletedAt = uc.todayString()
			t.RemainingHours = 0
		} else if status == task.StatusOpen {
			t.Assignments = []task.Assignment{}
		}
		t.Status = status
		t.UpdatedAt = uc.clock.Now().UTC()
		if err := uc.tasks.Update(txCtx, t); err != nil {
			return err
		}
		updated = t
		return nil
	})
	return updated, err
}

// DecideExtraTime approves or rejects a pending extra-time request without changing dueDate/dueTime.
func (uc *TaskUseCase) DecideExtraTime(ctx context.Context, claims auth.Claims, taskID string, approve bool) (task.Task, error) {
	now := uc.clock.Now().UTC()
	var updated task.Task
	var memberNotice *notification.Notice

	err := uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		t, err := uc.tasks.GetForUpdate(txCtx, claims.OrganizationID, taskID)
		if err != nil {
			return err
		}
		next, ok := engine.DecideExtraTime(t, approve)
		if !ok {
			return errs.WithCode(errs.KindConflict, "no_pending_request", "Tidak ada permintaan tambahan waktu yang menunggu.")
		}
		next.UpdatedAt = now
		if err := uc.tasks.Update(txCtx, next); err != nil {
			return err
		}
		updated = next

		if u, uErr := uc.auth.GetUserByMemberID(txCtx, claims.OrganizationID, next.ExtraTime.MemberID); uErr == nil {
			statusWord := "tidak disetujui"
			if approve {
				statusWord = fmt.Sprintf("disetujui (+%.1f jam)", next.ExtraTime.Hours)
			}
			memberNotice = &notification.Notice{
				ID:             fmt.Sprintf("waktu-dec-%s-%d", t.ID, now.UnixNano()),
				OrganizationID: claims.OrganizationID,
				UserID:         u.ID,
				Title:          fmt.Sprintf("Tambahan waktu %s: %s", statusWord, t.Title),
				Subtitle:       claims.Name + " telah meninjau permintaan tambahan waktumu.",
				Href:           "/saya",
				Kind:           notification.KindTaskResponse,
				CreatedAt:      now,
			}
		}
		return nil
	})
	if err != nil {
		return task.Task{}, err
	}
	if memberNotice != nil {
		_ = uc.notifs.Upsert(ctx, *memberNotice)
	}
	return updated, nil
}

// UpdateResultNote sets or clears the manager's plain-text result note on a completed task.
func (uc *TaskUseCase) UpdateResultNote(ctx context.Context, claims auth.Claims, taskID, note string) (task.Task, error) {
	var updated task.Task
	err := uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		t, err := uc.tasks.GetForUpdate(txCtx, claims.OrganizationID, taskID)
		if err != nil {
			return err
		}
		if t.Status != task.StatusDone {
			return errs.Validation("Catatan hasil hanya dapat dicatat untuk tugas yang sudah selesai.")
		}
		t.ResultNote = strings.TrimSpace(note)
		t.UpdatedAt = uc.clock.Now().UTC()
		if err := uc.tasks.Update(txCtx, t); err != nil {
			return err
		}
		updated = t
		return nil
	})
	return updated, err
}

// ReviewChangeRequest marks a freelancer's change request on a task as reviewed.
func (uc *TaskUseCase) ReviewChangeRequest(ctx context.Context, claims auth.Claims, taskID string) (task.Task, error) {
	var updated task.Task
	err := uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		t, err := uc.tasks.GetForUpdate(txCtx, claims.OrganizationID, taskID)
		if err != nil {
			return err
		}
		if t.ChangeRequest == nil || t.ChangeRequest.Status != "pending" {
			return errs.Conflict("Tidak ada permintaan perubahan yang menunggu tinjauan.")
		}
		cr := *t.ChangeRequest
		cr.Status = "reviewed"
		t.ChangeRequest = &cr
		t.UpdatedAt = uc.clock.Now().UTC()
		if err := uc.tasks.Update(txCtx, t); err != nil {
			return err
		}
		updated = t
		return nil
	})
	return updated, err
}

// ImportTaskRow represents the validation/import outcome for one CSV line.
type ImportTaskRow struct {
	Line  int        `json:"line"`
	Task  *task.Task `json:"task,omitempty"`
	Error string     `json:"error,omitempty"`
}

// ImportTasksCSV validates (dryRun=true) or persists (dryRun=false) bulk CSV tasks including external tasks.
func (uc *TaskUseCase) ImportTasksCSV(ctx context.Context, claims auth.Claims, csvText string, dryRun bool) ([]ImportTaskRow, error) {
	raw := strings.TrimSpace(csvText)
	if raw == "" {
		return nil, errs.Validation("Data CSV tidak boleh kosong.")
	}
	rawLines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	if len(rawLines) == 0 || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(rawLines[0])), "judul,") {
		return nil, errs.Validation("Baris pertama harus header: judul,proyek,tenggat,jam_tenggat,prioritas,dikelola_di,penanggung_jawab,jam,deskripsi.")
	}

	period, err := uc.ws.GetActivePeriod(ctx, claims.OrganizationID)
	if err != nil {
		return nil, err
	}
	projects, err := uc.projects.List(ctx, claims.OrganizationID, false)
	if err != nil {
		return nil, err
	}
	members, err := uc.members.List(ctx, claims.OrganizationID, period.ID, false)
	if err != nil {
		return nil, err
	}
	existingTasks, err := uc.tasks.List(ctx, claims.OrganizationID, period.ID, false)
	if err != nil {
		return nil, err
	}
	catalog, _ := uc.comps.List(ctx, claims.OrganizationID, false)

	projectExists := make(map[string]bool, len(projects))
	for _, p := range projects {
		projectExists[p.ID] = true
	}
	memberByEmail := make(map[string]member.Member, len(members))
	for _, m := range members {
		memberByEmail[strings.ToLower(m.Email)] = m
	}
	seen := make(map[string]bool, len(existingTasks))
	maxNum := 140
	for _, t := range existingTasks {
		seen[t.ProjectID+"|"+strings.ToLower(t.Title)] = true
		if strings.HasPrefix(t.ID, "TGS-") {
			if n, convErr := strconv.Atoi(strings.TrimPrefix(t.ID, "TGS-")); convErr == nil && n > maxNum {
				maxNum = n
			}
		}
	}

	today := uc.todayString()
	now := uc.clock.Now().UTC()
	nowStr := now.Format(time.RFC3339)
	previewSeq := maxNum + 1

	rows := make([]ImportTaskRow, 0)
	var validTasks []task.Task
	var validIndices []int

	for idx, rawLine := range rawLines[1:] {
		if strings.TrimSpace(rawLine) == "" {
			continue
		}
		lineNum := idx + 2
		cells := strings.Split(rawLine, ",")
		getCell := func(i int) string {
			if i < len(cells) {
				return strings.TrimSpace(cells[i])
			}
			return ""
		}
		title := getCell(0)
		projectID := getCell(1)
		dueDate := getCell(2)
		dueTime := getCell(3)
		prioStr := getCell(4)
		source := getCell(5)
		ownerEmail := getCell(6)
		hrsStr := getCell(7)
		desc := ""
		if len(cells) > 8 {
			desc = strings.TrimSpace(strings.Join(cells[8:], ","))
		}

		if title == "" {
			rows = append(rows, ImportTaskRow{Line: lineNum, Error: "Judul kosong"})
			continue
		}
		if !projectExists[projectID] {
			rows = append(rows, ImportTaskRow{Line: lineNum, Error: fmt.Sprintf("Proyek %q tidak ada", projectID)})
			continue
		}
		if _, pErr := time.Parse("2006-01-02", dueDate); pErr != nil || len(dueDate) != 10 {
			rows = append(rows, ImportTaskRow{Line: lineNum, Error: fmt.Sprintf("Tenggat %q bukan tanggal YYYY-MM-DD", dueDate)})
			continue
		}
		if dueDate < today {
			rows = append(rows, ImportTaskRow{Line: lineNum, Error: "Tenggat sudah lewat"})
			continue
		}
		if dueTime != "" && !dueTimeRe.MatchString(dueTime) {
			rows = append(rows, ImportTaskRow{Line: lineNum, Error: fmt.Sprintf("Jam tenggat %q bukan HH:MM", dueTime)})
			continue
		}
		if prioStr == "" {
			prioStr = string(task.PriorityNormal)
		}
		prio := task.Priority(prioStr)
		if !prio.IsValid() {
			rows = append(rows, ImportTaskRow{Line: lineNum, Error: fmt.Sprintf("Prioritas %q bukan rendah/normal/tinggi", prioStr)})
			continue
		}
		dupKey := projectID + "|" + strings.ToLower(title)
		if seen[dupKey] {
			rows = append(rows, ImportTaskRow{Line: lineNum, Error: "Duplikat: judul yang sama sudah ada di proyek ini"})
			continue
		}

		dt := dueTime
		if dt == "" {
			dt = "17:00"
		}
		built := task.Task{
			OrganizationID: claims.OrganizationID,
			ProjectID:      projectID,
			PeriodID:       period.ID,
			Title:          title,
			Description:    desc,
			DueDate:        dueDate,
			DueTime:        dt,
			Priority:       prio,
			CompetencyIDs:  []string{},
			CreatedAt:      nowStr,
			CreatedBy:      claims.UserID,
			UpdatedAt:      now,
		}

		if source != "" {
			m, ok := memberByEmail[strings.ToLower(ownerEmail)]
			if !ok {
				rows = append(rows, ImportTaskRow{Line: lineNum, Error: fmt.Sprintf("Penanggung jawab %q tidak terdaftar", ownerEmail)})
				continue
			}
			hrsVal, hErr := strconv.ParseFloat(strings.ReplaceAll(hrsStr, ",", "."), 64)
			if hErr != nil || hrsVal <= 0 {
				rows = append(rows, ImportTaskRow{Line: lineNum, Error: "Pekerjaan eksternal perlu jam lebih dari 0"})
				continue
			}
			built.Status = task.StatusRunning
			built.Assignments = []task.Assignment{{MemberID: m.ID, Hours: hrsVal}}
			built.RemainingHours = hrsVal
			built.ExternalSource = source
		} else {
			if len([]rune(desc)) < 20 {
				rows = append(rows, ImportTaskRow{Line: lineNum, Error: "Deskripsi minimal 20 karakter supaya kebutuhan bisa diurai"})
				continue
			}
			built.Status = task.StatusOpen
			built.Assignments = []task.Assignment{}
			built.RemainingHours = 0
			if !dryRun && uc.ai != nil {
				if dna, aErr := uc.ai.Analyze(ctx, title, desc, prio, catalog, existingTasks); aErr == nil {
					built.DNA = &dna
					built.RemainingHours = dna.EffortHours
				}
			}
		}

		seen[dupKey] = true
		built.ID = fmt.Sprintf("TGS-%04d", previewSeq)
		previewSeq++
		cp := built
		rows = append(rows, ImportTaskRow{Line: lineNum, Task: &cp})
		validTasks = append(validTasks, built)
		validIndices = append(validIndices, len(rows)-1)
	}

	if dryRun || len(validTasks) == 0 {
		return rows, nil
	}

	err = uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		for i, t := range validTasks {
			code, codeErr := uc.tasks.NextTaskCode(txCtx, claims.OrganizationID)
			if codeErr != nil {
				return codeErr
			}
			t.ID = code
			if err := uc.tasks.Create(txCtx, t); err != nil {
				return err
			}
			cp := t
			rows[validIndices[i]].Task = &cp
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (uc *TaskUseCase) BatchPreview(ctx context.Context, orgID string, preset workspace.PresetID) ([]engine.BatchPair, error) {
	members, tasks, settings, names, err := uc.loadEngineContext(ctx, orgID, "")
	if err != nil {
		return nil, err
	}
	if preset.IsValid() {
		settings.Preset = preset
	}
	var open []task.Task
	for _, t := range tasks {
		if t.Status == task.StatusOpen && t.DNA != nil && t.DNA.Confirmed {
			open = append(open, t)
		}
	}
	return engine.AllocateBatch(open, members, tasks, settings, names, uc.todayString()), nil
}

// BatchCommitItem specifies one task-member allocation chosen by manager from batch preview.
type BatchCommitItem struct {
	TaskID   string  `json:"taskId"`
	MemberID string  `json:"memberId"`
	Hours    float64 `json:"hours"`
}

func (uc *TaskUseCase) BatchCommit(ctx context.Context, claims auth.Claims, preset workspace.PresetID, items []BatchCommitItem) ([]task.Task, error) {
	if len(items) == 0 {
		return nil, errs.Validation("Pilih minimal satu tugas untuk ditetapkan.")
	}
	now := uc.clock.Now().UTC()
	var results []task.Task
	var notifs []notification.Notice

	err := uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		settings, err := uc.ws.GetEngineSettings(txCtx, claims.OrganizationID)
		if err != nil {
			return err
		}
		if preset.IsValid() {
			settings.Preset = preset
		}
		for _, item := range items {
			t, err := uc.tasks.GetForUpdate(txCtx, claims.OrganizationID, item.TaskID)
			if err != nil {
				return err
			}
			if t.Status != task.StatusOpen {
				return errs.Conflict(fmt.Sprintf("Tugas %s sudah tidak berstatus belum ditugaskan.", t.ID))
			}
			hrs := item.Hours
			if hrs <= 0 {
				hrs = t.EffortOrRemaining()
			}
			if hrs <= 0 {
				hrs = 4
			}
			code, err := uc.decs.NextDecisionCode(txCtx, claims.OrganizationID)
			if err != nil {
				return err
			}
			r1 := 1
			dec := decision.Decision{
				ID:             code,
				OrganizationID: claims.OrganizationID,
				TaskID:         t.ID,
				DecidedBy:      claims.UserID,
				MemberIDs:      []string{item.MemberID},
				Rank:           &r1,
				Preset:         settings.Preset,
				Note:           "Penugasan massal",
				DecidedAt:      now.Format(time.RFC3339),
				DecisionMs:     3 * 60_000,
				ConflictsSeen:  0,
			}
			t.Status = task.StatusWaiting
			t.Assignments = []task.Assignment{{MemberID: item.MemberID, Hours: hrs}}
			t.RemainingHours = hrs
			t.ResponseReason = ""
			t.UpdatedAt = now
			if err := uc.tasks.Update(txCtx, t); err != nil {
				return err
			}
			if err := uc.decs.Create(txCtx, dec); err != nil {
				return err
			}
			results = append(results, t)
			if u, uErr := uc.auth.GetUserByMemberID(txCtx, claims.OrganizationID, item.MemberID); uErr == nil {
				notifs = append(notifs, notification.Notice{
					ID:             fmt.Sprintf("assign-%s-%s", t.ID, item.MemberID),
					OrganizationID: claims.OrganizationID,
					UserID:         u.ID,
					Title:          "Penugasan baru: " + t.Title,
					Subtitle:       fmt.Sprintf("%s menetapkanmu lewat penugasan massal.", claims.Name),
					Href:           "/saya/tugas/" + t.ID,
					Kind:           notification.KindTaskAssigned,
					CreatedAt:      now,
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, n := range notifs {
		_ = uc.notifs.Upsert(ctx, n)
	}
	return results, nil
}
