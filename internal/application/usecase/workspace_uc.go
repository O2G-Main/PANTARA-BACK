package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/Orang2-BU/pantara-back/internal/application/port"
	"github.com/Orang2-BU/pantara-back/internal/domain/auth"
	"github.com/Orang2-BU/pantara-back/internal/domain/decision"
	"github.com/Orang2-BU/pantara-back/internal/domain/errs"
	"github.com/Orang2-BU/pantara-back/internal/domain/member"
	"github.com/Orang2-BU/pantara-back/internal/domain/project"
	"github.com/Orang2-BU/pantara-back/internal/domain/task"
	"github.com/Orang2-BU/pantara-back/internal/domain/workspace"
)

// BootstrapState matches all workspace domain collections required by the Next.js WorkspaceProvider.
type BootstrapState struct {
	Organization workspace.Organization   `json:"organization"`
	Period       workspace.WorkPeriod     `json:"period"`
	Periods      []workspace.WorkPeriod   `json:"periods"`
	Settings     workspace.EngineSettings `json:"settings"`
	Competencies []member.Competency      `json:"competencies"`
	Members      []member.Member          `json:"members"`
	Projects     []project.Project        `json:"projects"`
	Tasks        []task.Task              `json:"tasks"`
	Decisions    []decision.Decision      `json:"decisions"`
	Today        string                   `json:"today"`
	Now          string                   `json:"now"`
}

// BillingSummary summarizes workspace seat usage and plan metadata (P32).
type BillingSummary struct {
	Plan           string `json:"plan"`
	ActiveSeats    int    `json:"activeSeats"`
	IncludedSeats  int    `json:"includedSeats"`
	PendingInvites int    `json:"pendingInvites"`
	StatusLabel    string `json:"statusLabel"`
}

// WorkspaceUseCase handles org settings, periods, onboarding, recommendation presets, and billing.
type WorkspaceUseCase struct {
	tx      port.TxManager
	ws      port.WorkspaceRepository
	comps   port.CompetencyRepository
	members port.MemberRepository
	projects port.ProjectRepository
	tasks   port.TaskRepository
	decs    port.DecisionRepository
	auth    port.AuthRepository
	tokens  port.TokenService
	mailer  port.Mailer
	clock   port.Clock
	baseURL string
}

func NewWorkspaceUseCase(
	tx port.TxManager,
	ws port.WorkspaceRepository,
	comps port.CompetencyRepository,
	members port.MemberRepository,
	projects port.ProjectRepository,
	tasks port.TaskRepository,
	decs port.DecisionRepository,
	authRepo port.AuthRepository,
	tokens port.TokenService,
	mailer port.Mailer,
	clock port.Clock,
	baseURL string,
) *WorkspaceUseCase {
	return &WorkspaceUseCase{
		tx:       tx,
		ws:       ws,
		comps:    comps,
		members:  members,
		projects: projects,
		tasks:    tasks,
		decs:     decs,
		auth:     authRepo,
		tokens:   tokens,
		mailer:   mailer,
		clock:    clock,
		baseURL:  strings.TrimRight(baseURL, "/"),
	}
}

func orgNow(nowUTC string, org workspace.Organization) (string, string) {
	return nowUTC, nowUTC[:10]
}

// Bootstrap loads the workspace snapshot for the active (or specified) period scoped to claims.OrganizationID.
func (uc *WorkspaceUseCase) Bootstrap(ctx context.Context, claims auth.Claims, periodID string) (BootstrapState, error) {
	org, err := uc.ws.GetOrganization(ctx, claims.OrganizationID)
	if err != nil {
		return BootstrapState{}, err
	}
	periods, err := uc.ws.ListPeriods(ctx, claims.OrganizationID)
	if err != nil {
		return BootstrapState{}, err
	}
	var active workspace.WorkPeriod
	if periodID != "" {
		active, err = uc.ws.GetPeriod(ctx, claims.OrganizationID, periodID)
	} else {
		active, err = uc.ws.GetActivePeriod(ctx, claims.OrganizationID)
	}
	if err != nil {
		return BootstrapState{}, err
	}
	settings, err := uc.ws.GetEngineSettings(ctx, claims.OrganizationID)
	if err != nil {
		return BootstrapState{}, err
	}
	comps, err := uc.comps.List(ctx, claims.OrganizationID, false)
	if err != nil {
		return BootstrapState{}, err
	}
	members, err := uc.members.List(ctx, claims.OrganizationID, active.ID, false)
	if err != nil {
		return BootstrapState{}, err
	}
	projects, err := uc.projects.List(ctx, claims.OrganizationID, false)
	if err != nil {
		return BootstrapState{}, err
	}
	tasks, err := uc.tasks.List(ctx, claims.OrganizationID, active.ID, false)
	if err != nil {
		return BootstrapState{}, err
	}
	decisions, err := uc.decs.List(ctx, claims.OrganizationID)
	if err != nil {
		return BootstrapState{}, err
	}

	// Anggota only sees their own tasks in full detail while still receiving capacity overview safely.
	if claims.Role == auth.RoleMember {
		for i := range members {
			if members[i].ID != claims.MemberID {
				members[i].Email = ""
			}
		}
	}

	nowStr := uc.clock.Now().UTC().Format("2006-01-02T15:04:05Z07:00")
	return BootstrapState{
		Organization: org,
		Period:       active,
		Periods:      periods,
		Settings:     settings,
		Competencies: comps,
		Members:      members,
		Projects:     projects,
		Tasks:        tasks,
		Decisions:    decisions,
		Now:          nowStr,
		Today:        nowStr[:10],
	}, nil
}

func (uc *WorkspaceUseCase) GetOrganization(ctx context.Context, orgID string) (workspace.Organization, error) {
	return uc.ws.GetOrganization(ctx, orgID)
}

func (uc *WorkspaceUseCase) UpdateOrganization(ctx context.Context, orgID, name, timezone string, periodType workspace.PeriodType, hoursPerWeek float64, workDays, holidays []string) (workspace.Organization, error) {
	name = strings.TrimSpace(name)
	if len(name) < 3 {
		return workspace.Organization{}, errs.Validation("Nama organisasi minimal 3 karakter.")
	}
	if hoursPerWeek < 10 || hoursPerWeek > 60 {
		return workspace.Organization{}, errs.Validation("Jam kerja per minggu harus antara 10 dan 60 jam.")
	}
	org, err := uc.ws.GetOrganization(ctx, orgID)
	if err != nil {
		return workspace.Organization{}, err
	}
	org.Name = name
	if timezone != "" {
		org.Timezone = timezone
	}
	if periodType == workspace.PeriodWeekly || periodType == workspace.PeriodBiweekly {
		org.PeriodType = periodType
	}
	org.HoursPerWeek = hoursPerWeek
	if workDays != nil {
		org.WorkDays = workDays
	}
	if holidays != nil {
		org.Holidays = holidays
	}
	org.UpdatedAt = uc.clock.Now().UTC()
	if err := uc.ws.UpdateOrganization(ctx, org); err != nil {
		return workspace.Organization{}, err
	}
	return org, nil
}

// OnboardingInput represents the 3-step initial workspace setup wizard (P9).
type OnboardingInput struct {
	HoursPerWeek float64 `json:"hoursPerWeek"`
	PeriodType   workspace.PeriodType `json:"periodType"`
	Invites      []struct {
		Name  string `json:"name"`
		Email string `json:"email"`
		Role  string `json:"role"`
	} `json:"invites"`
}

// CompleteOnboarding persists workspace defaults and invites any imported team members atomically.
func (uc *WorkspaceUseCase) CompleteOnboarding(ctx context.Context, claims auth.Claims, in OnboardingInput) ([]member.Member, error) {
	org, err := uc.ws.GetOrganization(ctx, claims.OrganizationID)
	if err != nil {
		return nil, err
	}
	period, err := uc.ws.GetActivePeriod(ctx, claims.OrganizationID)
	if err != nil {
		return nil, err
	}
	if in.HoursPerWeek >= 10 && in.HoursPerWeek <= 60 {
		org.HoursPerWeek = in.HoursPerWeek
	}
	if in.PeriodType == workspace.PeriodWeekly || in.PeriodType == workspace.PeriodBiweekly {
		org.PeriodType = in.PeriodType
	}
	org.OnboardingDone = true
	org.UpdatedAt = uc.clock.Now().UTC()

	created := make([]member.Member, 0, len(in.Invites))
	existing, _ := uc.members.List(ctx, claims.OrganizationID, period.ID, true)
	usedSlugs := make(map[string]struct{}, len(existing))
	usedEmails := make(map[string]struct{}, len(existing))
	for _, m := range existing {
		usedSlugs[m.ID] = struct{}{}
		usedEmails[strings.ToLower(m.Email)] = struct{}{}
	}

	var inviteEmails []struct {
		inv       auth.InvitationToken
		acceptURL string
	}

	err = uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		if err := uc.ws.UpdateOrganization(txCtx, org); err != nil {
			return err
		}
		for _, item := range in.Invites {
			name := strings.TrimSpace(item.Name)
			email := strings.ToLower(strings.TrimSpace(item.Email))
			if len(name) < 3 || !emailRe.MatchString(email) {
				continue
			}
			if _, dup := usedEmails[email]; dup {
				continue
			}
			slug := slugify(name)
			base := slug
			n := 2
			for {
				if _, exists := usedSlugs[slug]; !exists {
					break
				}
				slug = fmt.Sprintf("%s-%d", base, n)
				n++
			}
			usedSlugs[slug] = struct{}{}
			usedEmails[email] = struct{}{}

			roleTitle := strings.TrimSpace(item.Role)
			if roleTitle == "" {
				roleTitle = "Pengembang perangkat lunak"
			}
			m := member.Member{
				ID:             slug,
				OrganizationID: claims.OrganizationID,
				Name:           name,
				Initials:       initialsOf(name),
				Role:           roleTitle,
				Email:          email,
				WeeklyLimit:    org.HoursPerWeek,
				Competencies:   []member.MemberCompetency{},
				Prefers:        []string{},
				Avoids:         []string{},
				LeaveDates:     []string{},
				Access:         auth.RoleMember,
				IsActive:       true,
				InvitePending:  true,
				CreatedAt:      uc.clock.Now().UTC(),
			}
			if err := uc.members.Create(txCtx, claims.OrganizationID, period.ID, m); err != nil {
				return err
			}
			rawTok, hashTok, tokErr := uc.tokens.NewOpaqueToken()
			if tokErr != nil {
				return tokErr
			}
			inv := auth.InvitationToken{
				ID:               uuid.NewString(),
				OrganizationID:   claims.OrganizationID,
				OrganizationName: org.Name,
				MemberID:         m.ID,
				Email:            m.Email,
				Name:             m.Name,
				RoleTitle:        m.Role,
				AccessRole:       auth.RoleMember,
				TokenHash:        hashTok,
				InvitedByUserID:  claims.UserID,
				InvitedByName:    claims.Name,
				ExpiresAt:        uc.clock.Now().UTC().Add(InviteTokenTTL),
				CreatedAt:        uc.clock.Now().UTC(),
			}
			if err := uc.auth.CreateInvitation(txCtx, inv); err != nil {
				return err
			}
			inviteEmails = append(inviteEmails, struct {
				inv       auth.InvitationToken
				acceptURL string
			}{inv: inv, acceptURL: fmt.Sprintf("%s/undangan/%s", uc.baseURL, rawTok)})
			created = append(created, m)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, em := range inviteEmails {
		_ = uc.mailer.SendInvitation(ctx, em.inv.Email, em.inv.Name, org.Name, claims.Name, em.acceptURL)
	}
	return created, nil
}

func (uc *WorkspaceUseCase) ListPeriods(ctx context.Context, orgID string) ([]workspace.WorkPeriod, error) {
	return uc.ws.ListPeriods(ctx, orgID)
}

func (uc *WorkspaceUseCase) CreatePeriod(ctx context.Context, orgID, label, startDate, endDate string, activate bool) (workspace.WorkPeriod, error) {
	if startDate == "" || endDate == "" || startDate > endDate {
		return workspace.WorkPeriod{}, errs.Validation("Rentang tanggal periode tidak valid.")
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = startDate + " - " + endDate
	}
	p := workspace.WorkPeriod{
		ID:             uuid.NewString(),
		OrganizationID: orgID,
		Code:           startDate,
		Label:          label,
		StartDate:      startDate,
		EndDate:        endDate,
		IsActive:       activate,
		CreatedAt:      uc.clock.Now().UTC(),
	}
	if err := uc.ws.CreatePeriod(ctx, p, activate); err != nil {
		return workspace.WorkPeriod{}, err
	}
	return p, nil
}

func (uc *WorkspaceUseCase) ActivatePeriod(ctx context.Context, orgID, periodID string) (workspace.WorkPeriod, error) {
	if err := uc.ws.ActivatePeriod(ctx, orgID, periodID); err != nil {
		return workspace.WorkPeriod{}, err
	}
	return uc.ws.GetPeriod(ctx, orgID, periodID)
}

func (uc *WorkspaceUseCase) GetSettings(ctx context.Context, orgID string) (workspace.EngineSettings, error) {
	return uc.ws.GetEngineSettings(ctx, orgID)
}

func (uc *WorkspaceUseCase) UpdateSettings(ctx context.Context, orgID string, s workspace.EngineSettings) (workspace.EngineSettings, error) {
	if !s.Preset.IsValid() {
		return workspace.EngineSettings{}, errs.Validation("Preset tidak dikenal.")
	}
	if s.LevelTolerance < 0 || s.LevelTolerance > 2 {
		return workspace.EngineSettings{}, errs.Validation("Toleransi level harus antara 0 dan 2.")
	}
	if s.OverloadAt < 0.6 || s.OverloadAt > 1.0 {
		return workspace.EngineSettings{}, errs.Validation("Ambang beban tinggi harus antara 60% dan 100%.")
	}
	if err := uc.ws.SaveEngineSettings(ctx, orgID, s); err != nil {
		return workspace.EngineSettings{}, err
	}
	return s, nil
}

func (uc *WorkspaceUseCase) GetBilling(ctx context.Context, orgID string) (BillingSummary, error) {
	org, err := uc.ws.GetOrganization(ctx, orgID)
	if err != nil {
		return BillingSummary{}, err
	}
	period, err := uc.ws.GetActivePeriod(ctx, orgID)
	if err != nil {
		return BillingSummary{}, err
	}
	members, err := uc.members.List(ctx, orgID, period.ID, false)
	if err != nil {
		return BillingSummary{}, err
	}
	pending, err := uc.auth.ListPendingInvitations(ctx, orgID)
	if err != nil {
		return BillingSummary{}, err
	}
	return BillingSummary{
		Plan:           org.SubscriptionPlan,
		ActiveSeats:    len(members),
		IncludedSeats:  25,
		PendingInvites: len(pending),
		StatusLabel:    "Masa pilot gratis sampai semua evaluasi selesai",
	}, nil
}
