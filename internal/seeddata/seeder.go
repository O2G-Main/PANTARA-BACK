package seeddata

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Orang2-BU/pantara-back/internal/application/port"
	"github.com/Orang2-BU/pantara-back/internal/domain/auth"
	"github.com/Orang2-BU/pantara-back/internal/domain/errs"
	"github.com/Orang2-BU/pantara-back/internal/domain/member"
	"github.com/Orang2-BU/pantara-back/internal/domain/notification"
	"github.com/Orang2-BU/pantara-back/internal/domain/task"
	"github.com/Orang2-BU/pantara-back/internal/domain/workspace"
)

// SeedStore exposes the minimal persistence contract needed by SeedArunika.
type SeedStore interface {
	port.TxManager
	port.AuthRepository
	port.WorkspaceRepository
	HasAnyOrganization(ctx context.Context) (bool, error)
	CreateUser(ctx context.Context, u auth.User) error
	SetSequenceCounters(ctx context.Context, orgID string, nextTaskSeq, nextDecisionSeq int) error
	EnsureMinTaskSequence(ctx context.Context, orgID string, minNext int) error
}

// SeedDeps holds repositories and helpers required to bootstrap Arunika Digital idempotently.
type SeedDeps struct {
	Store    SeedStore
	Members  port.MemberRepository
	Projects port.ProjectRepository
	Tasks    port.TaskRepository
	Decs     port.DecisionRepository
	Notifs   port.NotificationRepository
	HR       port.HRRepository
	Hasher   port.PasswordHasher
	Clock    port.Clock
}

// SeedOptions configures demo seeding behavior.
type SeedOptions struct {
	FixedDates bool // Keep literal 2026-09-28..2026-10-02 seed dates instead of shifting to the current week.
}

const (
	ArunikaOrgID     = "org-arunika"
	ArunikaOrgSlug   = "arunika-digital"
	DemoPassword     = "pantara2026"
	ActivePeriodID   = "prd-active"
	PreviousPeriodID = "prd-prev"
)

var indoMonthsShort = []string{"", "Jan", "Feb", "Mar", "Apr", "Mei", "Jun", "Jul", "Agu", "Sep", "Okt", "Nov", "Des"}

// SeedArunika populates the canonical Arunika Digital dataset + demo logins when the DB has no organizations yet.
func SeedArunika(ctx context.Context, deps SeedDeps, opts SeedOptions) (bool, error) {
	exists, err := deps.Store.HasAnyOrganization(ctx)
	if err != nil {
		return false, fmt.Errorf("check existing organizations: %w", err)
	}
	if exists {
		return UpgradeArunikaIfPresent(ctx, deps)
	}

	ds, err := LoadArunika()
	if err != nil {
		return false, fmt.Errorf("load arunika seed: %w", err)
	}

	now := deps.Clock.Now().UTC()
	deltaDays := 0
	if !opts.FixedDates {
		baseMonday, err := time.Parse("2006-01-02", ds.Period.Start)
		if err == nil {
			jakarta := time.FixedZone("WIB", 7*3600)
			localNow := deps.Clock.Now().In(jakarta)
			offsetToMonday := -(int(localNow.Weekday()) - 1)
			switch localNow.Weekday() {
			case time.Saturday:
				offsetToMonday = 2 // roll weekend demo seeding to upcoming workweek
			case time.Sunday:
				offsetToMonday = 1
			}
			thisMonday := time.Date(localNow.Year(), localNow.Month(), localNow.Day()+offsetToMonday, 0, 0, 0, 0, time.UTC)
			deltaDays = int(thisMonday.Sub(baseMonday).Hours() / 24)
		}
	}

	shiftDate := func(ymd string) string {
		if ymd == "" || deltaDays == 0 {
			return ymd
		}
		t, err := time.Parse("2006-01-02", ymd)
		if err != nil {
			return ymd
		}
		return t.AddDate(0, 0, deltaDays).Format("2006-01-02")
	}

	shiftTimestamp := func(raw string) string {
		if raw == "" || deltaDays == 0 {
			return raw
		}
		if len(raw) == 10 {
			return shiftDate(raw)
		}
		if t, err := time.Parse(time.RFC3339, raw); err == nil {
			return t.AddDate(0, 0, deltaDays).Format(time.RFC3339)
		}
		return raw
	}

	activeStart := shiftDate(ds.Period.Start)
	activeEnd := shiftDate(ds.Period.End)
	activeLabel := ds.Period.Label
	if deltaDays != 0 {
		activeLabel = formatPeriodLabel(activeStart, activeEnd)
	}

	prevStart := shiftDateBy(activeStart, -7)
	prevEnd := shiftDateBy(activeEnd, -7)
	prevLabel := formatPeriodLabel(prevStart, prevEnd)

	pwHash, err := deps.Hasher.Hash(DemoPassword)
	if err != nil {
		return false, fmt.Errorf("hash demo password: %w", err)
	}

	err = deps.Store.WithinTx(ctx, func(txCtx context.Context) error {
		org := workspace.Organization{
			ID:               ArunikaOrgID,
			Slug:             ArunikaOrgSlug,
			Name:             ds.Workspace.Name,
			Timezone:         "Asia/Jakarta",
			PeriodType:       workspace.PeriodWeekly,
			HoursPerWeek:     ds.Workspace.HoursPerWeek,
			WorkDays:         []string{"Sen", "Sel", "Rab", "Kam", "Jum"},
			Holidays:         []string{},
			BaselineMinutes:  ds.BaselineMinutes,
			SubscriptionPlan: "pilot",
			OnboardingDone:   true,
			CreatedAt:        now,
			UpdatedAt:        now,
		}

		activePeriod := workspace.WorkPeriod{
			ID:             ActivePeriodID,
			OrganizationID: ArunikaOrgID,
			Code:           "PRD-AKTIF",
			Label:          activeLabel,
			StartDate:      activeStart,
			EndDate:        activeEnd,
			IsActive:       true,
			CreatedAt:      now,
		}
		prevPeriod := workspace.WorkPeriod{
			ID:             PreviousPeriodID,
			OrganizationID: ArunikaOrgID,
			Code:           "PRD-LALU",
			Label:          prevLabel,
			StartDate:      prevStart,
			EndDate:        prevEnd,
			IsActive:       false,
			CreatedAt:      now.Add(-7 * 24 * time.Hour),
		}

		comps := make([]member.Competency, len(ds.Competencies))
		for i, c := range ds.Competencies {
			c.OrganizationID = ArunikaOrgID
			c.CreatedAt = now
			comps[i] = c
		}

		adminUser := auth.User{
			ID:             "usr-admin-arunika",
			OrganizationID: ArunikaOrgID,
			MemberID:       "",
			Email:          "krisna@pantara.id",
			Name:           "Krisna (Admin)",
			PasswordHash:   pwHash,
			Role:           auth.RoleAdmin,
			IsActive:       true,
			SessionVersion: 1,
			NotifyPrefs:    auth.DefaultNotifyPrefs(),
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := deps.Store.CreateWorkspaceBootstrap(txCtx, org, activePeriod, workspace.DefaultEngineSettings(), comps, adminUser); err != nil {
			return err
		}
		if err := deps.Store.CreatePeriod(txCtx, prevPeriod, false); err != nil {
			return err
		}

		managerUser := auth.User{
			ID:             "usr-manajer-arunika",
			OrganizationID: ArunikaOrgID,
			MemberID:       "",
			Email:          "manajer@pantara.id",
			Name:           "Raka Pratama (Manajer)",
			PasswordHash:   pwHash,
			Role:           auth.RoleManager,
			IsActive:       true,
			SessionVersion: 1,
			NotifyPrefs:    auth.DefaultNotifyPrefs(),
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := deps.Store.CreateUser(txCtx, managerUser); err != nil {
			return err
		}

		hrUser := auth.User{
			ID:             "usr-hr-arunika",
			OrganizationID: ArunikaOrgID,
			MemberID:       "",
			Email:          "hr@pantara.id",
			Name:           "Nadia Putri (HR)",
			PasswordHash:   pwHash,
			Role:           auth.RoleHR,
			IsActive:       true,
			SessionVersion: 1,
			NotifyPrefs:    auth.DefaultNotifyPrefs(),
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := deps.Store.CreateUser(txCtx, hrUser); err != nil {
			return err
		}

		if deps.HR != nil {
			if err := deps.HR.SaveHRSettings(txCtx, ArunikaOrgID, workspace.DefaultHRSettings()); err != nil {
				return err
			}
			if err := deps.HR.SavePerformanceConfig(txCtx, ArunikaOrgID, workspace.DefaultPerformanceConfig()); err != nil {
				return err
			}
		}

		bimaUser := auth.User{
			ID:             "usr-member-bima",
			OrganizationID: ArunikaOrgID,
			MemberID:       "bima",
			Email:          "bima@pantara.id",
			Name:           "Bima Pradana",
			PasswordHash:   pwHash,
			Role:           auth.RoleMember,
			IsActive:       true,
			SessionVersion: 1,
			NotifyPrefs:    auth.DefaultNotifyPrefs(),
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := deps.Store.CreateUser(txCtx, bimaUser); err != nil {
			return err
		}

		for _, m := range ds.Members {
			m.OrganizationID = ArunikaOrgID
			if m.Initials == "" {
				m.Initials = makeInitials(m.Name)
			}
			if m.Email == "" {
				m.Email = fmt.Sprintf("%s@pantara.id", m.ID)
			}
			if m.ID == "bima" {
				m.UserID = bimaUser.ID
				m.Email = bimaUser.Email
			}
			if !m.Access.IsValid() {
				m.Access = auth.RoleMember
			}
			if !m.EmploymentType.IsValid() {
				m.EmploymentType = member.EmploymentEmployee
			}
			m.IsActive = true
			shiftedLeave := make([]string, len(m.LeaveDates))
			for idx, ld := range m.LeaveDates {
				shiftedLeave[idx] = shiftDate(ld)
			}
			m.LeaveDates = shiftedLeave
			if len(m.LeaveRequests) > 0 {
				reqs := make([]member.LeaveRequest, len(m.LeaveRequests))
				for idx, lr := range m.LeaveRequests {
					lr.MemberID = m.ID
					lr.Start = shiftDate(lr.Start)
					lr.End = shiftDate(lr.End)
					lr.RequestedAt = shiftTimestamp(lr.RequestedAt)
					reqs[idx] = lr
				}
				m.LeaveRequests = reqs
			}
			if err := deps.Members.Create(txCtx, ArunikaOrgID, ActivePeriodID, m); err != nil {
				return err
			}
			// Seed a baseline previous-period availability snapshot so "Salin dari periode lalu" works cleanly.
			if err := deps.Members.SaveAvailability(txCtx, ArunikaOrgID, PreviousPeriodID, m.ID, m.AvailableHours, m.WeeklyLimit, nil); err != nil {
				return err
			}
		}

		maxTaskSeq := 142
		maxDecSeq := 39

		for _, p := range ds.Projects {
			p.OrganizationID = ArunikaOrgID
			p.DueDate = shiftDate(p.DueDate)
			p.CreatedAt = now
			p.UpdatedAt = now
			if err := deps.Projects.Create(txCtx, p); err != nil {
				return err
			}
		}

		for _, t := range ds.Tasks {
			t.OrganizationID = ArunikaOrgID
			t.PeriodID = ActivePeriodID
			t.DueDate = shiftDate(t.DueDate)
			t.CompletedAt = shiftDate(t.CompletedAt)
			t.CreatedAt = shiftTimestamp(t.CreatedAt)
			if t.ExtraTime != nil {
				et := *t.ExtraTime
				et.RequestedAt = shiftTimestamp(et.RequestedAt)
				t.ExtraTime = &et
			}
			t.CreatedBy = adminUser.ID
			t.UpdatedAt = now
			if seq := parseCodeNum(t.ID, "TGS-"); seq > maxTaskSeq {
				maxTaskSeq = seq
			}
			if err := deps.Tasks.Create(txCtx, t); err != nil {
				return err
			}
		}

		for _, d := range ds.Decisions {
			d.OrganizationID = ArunikaOrgID
			if d.DecidedBy == "" {
				d.DecidedBy = managerUser.ID
			}
			d.DecidedAt = shiftTimestamp(d.DecidedAt)
			if seq := parseCodeNum(d.ID, "KPT-"); seq > maxDecSeq {
				maxDecSeq = seq
			}
			if err := deps.Decs.Create(txCtx, d); err != nil {
				return err
			}
		}

		if err := deps.Store.SetSequenceCounters(txCtx, ArunikaOrgID, maxTaskSeq+1, maxDecSeq+1); err != nil {
			return err
		}

		if deps.Notifs != nil {
			starterNotices := []notification.Notice{
				{
					ID:             "ntf-seed-tgs-0142",
					OrganizationID: ArunikaOrgID,
					UserID:         adminUser.ID,
					Title:          "TGS-0142 perlu ditugaskan",
					Subtitle:       "Integrasi SSO + OAuth2 · Tenggat " + activeEnd,
					Href:           "/tugas/TGS-0142",
					Kind:           notification.KindOperational,
					CreatedAt:      now,
				},
				{
					ID:             "ntf-seed-manager-tgs-0142",
					OrganizationID: ArunikaOrgID,
					UserID:         managerUser.ID,
					Title:          "TGS-0142 siap ditugaskan",
					Subtitle:       "Gunakan Analisa Task DNA atau pilih kandidat rekomendasi",
					Href:           "/tugas/TGS-0142",
					Kind:           notification.KindOperational,
					CreatedAt:      now,
				},
				{
					ID:             "ntf-seed-bima-welcome",
					OrganizationID: ArunikaOrgID,
					UserID:         bimaUser.ID,
					Title:          "Selamat datang di periode " + activeLabel,
					Subtitle:       "Periksa tugas aktif dan perbarui ketersediaan minggu ini",
					Href:           "/saya",
					Kind:           notification.KindOperational,
					CreatedAt:      now,
				},
			}
			for _, n := range starterNotices {
				if err := deps.Notifs.Upsert(txCtx, n); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// UpgradeArunikaIfPresent idempotently backfills Corporate-First / HR additions into an
// already-deployed org-arunika database without resetting or overwriting existing data.
func UpgradeArunikaIfPresent(ctx context.Context, deps SeedDeps) (bool, error) {
	if _, err := deps.Store.GetOrganization(ctx, ArunikaOrgID); err != nil {
		if errs.KindOf(err) == errs.KindNotFound {
			return false, nil
		}
		return false, err
	}
	activePeriod, err := deps.Store.GetActivePeriod(ctx, ArunikaOrgID)
	if err != nil {
		return false, err
	}

	ds, err := LoadArunika()
	if err != nil {
		return false, fmt.Errorf("load arunika seed: %w", err)
	}

	deltaDays := 0
	if baseMonday, err1 := time.Parse("2006-01-02", ds.Period.Start); err1 == nil {
		if activeStart, err2 := time.Parse("2006-01-02", activePeriod.StartDate); err2 == nil {
			deltaDays = int(activeStart.Sub(baseMonday).Hours() / 24)
		}
	}
	shiftDate := func(ymd string) string {
		if ymd == "" || deltaDays == 0 {
			return ymd
		}
		return shiftDateBy(ymd, deltaDays)
	}
	shiftTimestamp := func(raw string) string {
		if raw == "" || deltaDays == 0 {
			return raw
		}
		if len(raw) == 10 {
			return shiftDate(raw)
		}
		if t, parseErr := time.Parse(time.RFC3339, raw); parseErr == nil {
			return t.AddDate(0, 0, deltaDays).Format(time.RFC3339)
		}
		return raw
	}

	seedMembersByID := make(map[string]member.Member, len(ds.Members))
	for _, sm := range ds.Members {
		seedMembersByID[sm.ID] = sm
	}
	seedTasksByID := make(map[string]task.Task, len(ds.Tasks))
	for _, st := range ds.Tasks {
		seedTasksByID[st.ID] = st
	}

	now := deps.Clock.Now().UTC()
	changed := false

	err = deps.Store.WithinTx(ctx, func(txCtx context.Context) error {
		if _, uErr := deps.Store.GetUserByEmail(txCtx, "hr@pantara.id"); errs.KindOf(uErr) == errs.KindNotFound {
			pwHash, hErr := deps.Hasher.Hash(DemoPassword)
			if hErr != nil {
				return fmt.Errorf("hash demo password: %w", hErr)
			}
			hrUser := auth.User{
				ID:             "usr-hr-arunika",
				OrganizationID: ArunikaOrgID,
				MemberID:       "",
				Email:          "hr@pantara.id",
				Name:           "Nadia Putri (HR)",
				PasswordHash:   pwHash,
				Role:           auth.RoleHR,
				IsActive:       true,
				SessionVersion: 1,
				NotifyPrefs:    auth.DefaultNotifyPrefs(),
				CreatedAt:      now,
				UpdatedAt:      now,
			}
			if cErr := deps.Store.CreateUser(txCtx, hrUser); cErr != nil {
				return cErr
			}
			if deps.HR != nil {
				if err := deps.HR.SaveHRSettings(txCtx, ArunikaOrgID, workspace.DefaultHRSettings()); err != nil {
					return err
				}
				if err := deps.HR.SavePerformanceConfig(txCtx, ArunikaOrgID, workspace.DefaultPerformanceConfig()); err != nil {
					return err
				}
			}
			changed = true
		} else if uErr != nil {
			return uErr
		}

		existingMembers, mErr := deps.Members.List(txCtx, ArunikaOrgID, activePeriod.ID, true)
		if mErr != nil {
			return mErr
		}
		for _, m := range existingMembers {
			sm, ok := seedMembersByID[m.ID]
			if !ok {
				continue
			}
			dirty := false
			if sm.EmploymentType == member.EmploymentFreelancer && m.EmploymentType != member.EmploymentFreelancer {
				m.EmploymentType = member.EmploymentFreelancer
				dirty = true
			}
			if m.ID == "fikri" && len(m.LeaveRequests) == 0 && len(sm.LeaveRequests) > 0 {
				reqs := make([]member.LeaveRequest, len(sm.LeaveRequests))
				for i, lr := range sm.LeaveRequests {
					lr.MemberID = m.ID
					lr.Start = shiftDate(lr.Start)
					lr.End = shiftDate(lr.End)
					lr.RequestedAt = shiftTimestamp(lr.RequestedAt)
					reqs[i] = lr
				}
				m.LeaveRequests = reqs
				dirty = true
			}
			if dirty {
				m.UpdatedAt = now
				if err := deps.Members.UpdateProfile(txCtx, ArunikaOrgID, m); err != nil {
					return err
				}
				changed = true
			}
		}

		existingTasks, tErr := deps.Tasks.List(txCtx, ArunikaOrgID, "", true)
		if tErr != nil {
			return tErr
		}
		taskByID := make(map[string]task.Task, len(existingTasks))
		for _, t := range existingTasks {
			taskByID[t.ID] = t
		}

		if extSeed, ok := seedTasksByID["TGS-0174"]; ok {
			if _, exists := taskByID["TGS-0174"]; !exists {
				extSeed.OrganizationID = ArunikaOrgID
				extSeed.PeriodID = activePeriod.ID
				extSeed.DueDate = shiftDate(extSeed.DueDate)
				extSeed.CreatedAt = shiftTimestamp(extSeed.CreatedAt)
				extSeed.CreatedBy = "usr-admin-arunika"
				extSeed.UpdatedAt = now
				if err := deps.Tasks.Create(txCtx, extSeed); err != nil {
					return err
				}
				if err := deps.Store.EnsureMinTaskSequence(txCtx, ArunikaOrgID, 175); err != nil {
					return err
				}
				changed = true
			}
		}

		if t164, exists := taskByID["TGS-0164"]; exists && t164.ExtraTime == nil {
			if s164, ok := seedTasksByID["TGS-0164"]; ok && s164.ExtraTime != nil {
				et := *s164.ExtraTime
				et.RequestedAt = shiftTimestamp(et.RequestedAt)
				t164.ExtraTime = &et
				t164.UpdatedAt = now
				if err := deps.Tasks.Update(txCtx, t164); err != nil {
					return err
				}
				changed = true
			}
		}

		for id, cur := range taskByID {
			if cur.Status != task.StatusDone {
				continue
			}
			st, ok := seedTasksByID[id]
			if !ok {
				continue
			}
			dirty := false
			if cur.CompletedAt == "" && st.CompletedAt != "" {
				cur.CompletedAt = shiftDate(st.CompletedAt)
				dirty = true
			}
			if cur.QualityScore == nil && st.QualityScore != nil {
				q := *st.QualityScore
				cur.QualityScore = &q
				dirty = true
			}
			if dirty {
				cur.UpdatedAt = now
				if err := deps.Tasks.Update(txCtx, cur); err != nil {
					return err
				}
				changed = true
			}
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return changed, nil
}

func makeInitials(name string) string {
	parts := strings.Fields(strings.TrimSpace(name))
	if len(parts) == 0 {
		return "NA"
	}
	if len(parts) == 1 {
		r := []rune(parts[0])
		if len(r) >= 2 {
			return strings.ToUpper(string(r[:2]))
		}
		return strings.ToUpper(string(r))
	}
	first := []rune(parts[0])[0]
	last := []rune(parts[len(parts)-1])[0]
	return strings.ToUpper(string([]rune{first, last}))
}

func shiftDateBy(ymd string, days int) string {
	t, err := time.Parse("2006-01-02", ymd)
	if err != nil {
		return ymd
	}
	return t.AddDate(0, 0, days).Format("2006-01-02")
}

func formatPeriodLabel(startYMD, endYMD string) string {
	s, err1 := time.Parse("2006-01-02", startYMD)
	e, err2 := time.Parse("2006-01-02", endYMD)
	if err1 != nil || err2 != nil {
		return fmt.Sprintf("%s - %s", startYMD, endYMD)
	}
	if s.Month() == e.Month() && s.Year() == e.Year() {
		return fmt.Sprintf("%d - %d %s %d", s.Day(), e.Day(), indoMonthsShort[int(e.Month())], e.Year())
	}
	return fmt.Sprintf("%d %s - %d %s %d", s.Day(), indoMonthsShort[int(s.Month())], e.Day(), indoMonthsShort[int(e.Month())], e.Year())
}

func parseCodeNum(id, prefix string) int {
	if !strings.HasPrefix(id, prefix) {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimPrefix(id, prefix))
	if err != nil {
		return 0
	}
	return n
}

