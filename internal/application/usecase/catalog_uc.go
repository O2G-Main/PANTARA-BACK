package usecase

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/Orang2-BU/pantara-back/internal/application/port"
	"github.com/Orang2-BU/pantara-back/internal/domain/auth"
	"github.com/Orang2-BU/pantara-back/internal/domain/engine"
	"github.com/Orang2-BU/pantara-back/internal/domain/errs"
	"github.com/Orang2-BU/pantara-back/internal/domain/member"
	"github.com/Orang2-BU/pantara-back/internal/domain/notification"
	"github.com/Orang2-BU/pantara-back/internal/domain/project"
	"github.com/Orang2-BU/pantara-back/internal/domain/task"
)

// CatalogUseCase orchestrates full CRUD for Competencies, Projects, and Team Members.
type CatalogUseCase struct {
	tx       port.TxManager
	ws       port.WorkspaceRepository
	comps    port.CompetencyRepository
	members  port.MemberRepository
	projects port.ProjectRepository
	tasks    port.TaskRepository
	auth     port.AuthRepository
	notifs   port.NotificationRepository
	hr       port.HRRepository
	tokens   port.TokenService
	mailer   port.Mailer
	sec      port.SecurityStore
	clock    port.Clock
	baseURL  string
}

func NewCatalogUseCase(
	tx port.TxManager,
	ws port.WorkspaceRepository,
	comps port.CompetencyRepository,
	members port.MemberRepository,
	projects port.ProjectRepository,
	tasks port.TaskRepository,
	authRepo port.AuthRepository,
	notifs port.NotificationRepository,
	hr port.HRRepository,
	tokens port.TokenService,
	mailer port.Mailer,
	sec port.SecurityStore,
	clock port.Clock,
	baseURL string,
) *CatalogUseCase {
	return &CatalogUseCase{
		tx:       tx,
		ws:       ws,
		comps:    comps,
		members:  members,
		projects: projects,
		tasks:    tasks,
		auth:     authRepo,
		notifs:   notifs,
		hr:       hr,
		tokens:   tokens,
		mailer:   mailer,
		sec:      sec,
		clock:    clock,
		baseURL:  strings.TrimRight(baseURL, "/"),
	}
}

// --- Competencies (P30) ---

func (uc *CatalogUseCase) ListCompetencies(ctx context.Context, orgID string) ([]member.Competency, error) {
	return uc.comps.List(ctx, orgID, false)
}

func (uc *CatalogUseCase) CreateCompetency(ctx context.Context, orgID, name, category string) (member.Competency, error) {
	name = strings.TrimSpace(name)
	category = strings.TrimSpace(category)
	if len(name) < 2 {
		return member.Competency{}, errs.Validation("Tulis nama kompetensi.")
	}
	if category == "" {
		category = "Lainnya"
	}
	id := slugify(name)
	existing, err := uc.comps.List(ctx, orgID, true)
	if err != nil {
		return member.Competency{}, err
	}
	for _, c := range existing {
		if c.ID == id {
			if !c.IsArchived {
				return member.Competency{}, errs.Conflict("Kompetensi ini sudah ada di katalog.")
			}
			c.Name = name
			c.Category = category
			c.IsArchived = false
			if err := uc.comps.Update(ctx, c); err != nil {
				return member.Competency{}, err
			}
			return c, nil
		}
	}
	c := member.Competency{
		ID:             id,
		OrganizationID: orgID,
		Name:           name,
		Category:       category,
		CreatedAt:      uc.clock.Now().UTC(),
	}
	if err := uc.comps.Create(ctx, c); err != nil {
		return member.Competency{}, err
	}
	return c, nil
}

func (uc *CatalogUseCase) UpdateCompetency(ctx context.Context, orgID, id, name, category string) (member.Competency, error) {
	c, err := uc.comps.Get(ctx, orgID, id)
	if err != nil {
		return member.Competency{}, err
	}
	name = strings.TrimSpace(name)
	if len(name) < 2 {
		return member.Competency{}, errs.Validation("Tulis nama kompetensi.")
	}
	c.Name = name
	if strings.TrimSpace(category) != "" {
		c.Category = strings.TrimSpace(category)
	}
	if err := uc.comps.Update(ctx, c); err != nil {
		return member.Competency{}, err
	}
	return c, nil
}

func (uc *CatalogUseCase) DeleteCompetency(ctx context.Context, orgID, id string) error {
	if _, err := uc.comps.Get(ctx, orgID, id); err != nil {
		return err
	}
	period, err := uc.ws.GetActivePeriod(ctx, orgID)
	if err == nil {
		tasks, _ := uc.tasks.List(ctx, orgID, period.ID, false)
		for _, t := range tasks {
			if t.Status != task.StatusDone && t.DNA != nil {
				for _, r := range t.DNA.Requirements {
					if r.CompetencyID == id {
						return errs.Conflict("Kompetensi masih dipakai oleh tugas aktif.")
					}
				}
			}
		}
	}
	return uc.comps.ArchiveOrDelete(ctx, orgID, id)
}

// --- Projects (P20) ---

func (uc *CatalogUseCase) ListProjects(ctx context.Context, orgID string) ([]project.Project, error) {
	return uc.projects.List(ctx, orgID, false)
}

func (uc *CatalogUseCase) GetProject(ctx context.Context, orgID, id string) (project.Project, error) {
	return uc.projects.Get(ctx, orgID, id)
}

func (uc *CatalogUseCase) CreateProject(ctx context.Context, orgID, name, client, dueDate string) (project.Project, error) {
	name = strings.TrimSpace(name)
	client = strings.TrimSpace(client)
	if len(name) < 3 {
		return project.Project{}, errs.Validation("Tulis nama proyek minimal 3 karakter.")
	}
	if client == "" {
		client = "Internal"
	}
	if dueDate == "" {
		return project.Project{}, errs.Validation("Pilih tenggat proyek.")
	}
	existing, err := uc.projects.List(ctx, orgID, true)
	if err != nil {
		return project.Project{}, err
	}
	slug := slugify(name)
	used := make(map[string]struct{}, len(existing))
	for _, p := range existing {
		used[p.ID] = struct{}{}
	}
	base := slug
	for n := 2; ; n++ {
		if _, exists := used[slug]; !exists {
			break
		}
		slug = fmt.Sprintf("%s-%d", base, n)
	}
	now := uc.clock.Now().UTC()
	p := project.Project{
		ID:             slug,
		OrganizationID: orgID,
		Name:           name,
		Client:         client,
		DueDate:        dueDate,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := uc.projects.Create(ctx, p); err != nil {
		return project.Project{}, err
	}
	return p, nil
}

func (uc *CatalogUseCase) UpdateProject(ctx context.Context, orgID, id, name, client, dueDate string) (project.Project, error) {
	p, err := uc.projects.Get(ctx, orgID, id)
	if err != nil {
		return project.Project{}, err
	}
	name = strings.TrimSpace(name)
	if len(name) < 3 {
		return project.Project{}, errs.Validation("Tulis nama proyek minimal 3 karakter.")
	}
	p.Name = name
	if strings.TrimSpace(client) != "" {
		p.Client = strings.TrimSpace(client)
	}
	if dueDate != "" {
		p.DueDate = dueDate
	}
	p.UpdatedAt = uc.clock.Now().UTC()
	if err := uc.projects.Update(ctx, p); err != nil {
		return project.Project{}, err
	}
	return p, nil
}

func (uc *CatalogUseCase) DeleteProject(ctx context.Context, orgID, id string) error {
	if _, err := uc.projects.Get(ctx, orgID, id); err != nil {
		return err
	}
	period, err := uc.ws.GetActivePeriod(ctx, orgID)
	if err == nil {
		tasks, _ := uc.tasks.List(ctx, orgID, period.ID, false)
		for _, t := range tasks {
			if t.ProjectID == id && t.Status != task.StatusDone {
				return errs.Conflict("Proyek masih memiliki tugas aktif yang belum selesai.")
			}
		}
	}
	return uc.projects.ArchiveOrDelete(ctx, orgID, id)
}

// --- Members & Team (P17, P19, P31) ---

func (uc *CatalogUseCase) ListMembers(ctx context.Context, orgID, periodID string) ([]member.Member, error) {
	if periodID == "" {
		p, err := uc.ws.GetActivePeriod(ctx, orgID)
		if err != nil {
			return nil, err
		}
		periodID = p.ID
	}
	return uc.members.List(ctx, orgID, periodID, false)
}

func (uc *CatalogUseCase) ListMembersWithSettings(ctx context.Context, orgID string) ([]member.Member, []auth.InvitationToken, error) {
	p, err := uc.ws.GetActivePeriod(ctx, orgID)
	if err != nil {
		return nil, nil, err
	}
	members, err := uc.members.List(ctx, orgID, p.ID, true)
	if err != nil {
		return nil, nil, err
	}
	invites, err := uc.auth.ListPendingInvitations(ctx, orgID)
	if err != nil {
		return nil, nil, err
	}
	return members, invites, nil
}

func (uc *CatalogUseCase) GetMember(ctx context.Context, orgID, periodID, id string) (member.Member, error) {
	if periodID == "" {
		p, err := uc.ws.GetActivePeriod(ctx, orgID)
		if err != nil {
			return member.Member{}, err
		}
		periodID = p.ID
	}
	return uc.members.Get(ctx, orgID, periodID, id)
}

// CreateMemberInput captures member creation parameters from managers, admins, or HR.
type CreateMemberInput struct {
	Name                 string                `json:"name"`
	Email                string                `json:"email"`
	RoleTitle            string                `json:"role"`
	Access               auth.Role             `json:"access"`
	EmploymentType       member.EmploymentType `json:"employmentType"`
	Team                 string                `json:"team"`
	Division             string                `json:"division"`
	WeeklyLimit          *float64              `json:"weeklyLimit"`
	WorkHoursOverride    *float64              `json:"workHoursOverride"`
	ReserveHoursOverride *float64              `json:"reserveHoursOverride"`
	AvailableHours       *float64              `json:"availableHours"`
	IsActive             *bool                 `json:"isActive"`
}

// UpdateMemberInput captures partial member profile edits; HR-only fields are guarded by caller role.
type UpdateMemberInput struct {
	Name                    *string
	RoleTitle               *string
	Email                   *string
	WeeklyLimit             *float64
	EmploymentType          *member.EmploymentType
	Team                    *string
	Division                *string
	WorkHoursOverride       *float64
	HasWorkHoursOverride    bool
	ReserveHoursOverride    *float64
	HasReserveHoursOverride bool
	AvailableHours          *float64
	HasAvailableHours       bool
	IsActive                *bool
}

// ImportMemberRow represents the validation/import outcome for one CSV line.
type ImportMemberRow struct {
	Line   int            `json:"line"`
	Member *member.Member `json:"member,omitempty"`
	Error  string         `json:"error,omitempty"`
}

// CreateAndInviteMember registers a new team member and issues a single-use invitation token.
func (uc *CatalogUseCase) CreateAndInviteMember(ctx context.Context, claims auth.Claims, in CreateMemberInput) (member.Member, string, error) {
	name := strings.TrimSpace(in.Name)
	email := strings.ToLower(strings.TrimSpace(in.Email))
	roleTitle := strings.TrimSpace(in.RoleTitle)
	if len(name) < 2 {
		return member.Member{}, "", errs.Validation("Tulis nama lengkap karyawan.")
	}
	if !emailRe.MatchString(email) {
		return member.Member{}, "", errs.Validation("Email belum valid.")
	}
	if roleTitle == "" {
		roleTitle = "Pengembang perangkat lunak"
	}
	access := in.Access
	if !access.IsValid() {
		access = auth.RoleMember
	}
	if access != auth.RoleMember && !claims.Role.CanAdminister() {
		return member.Member{}, "", errs.Forbidden("Hanya admin yang dapat menetapkan peran manajer, HR, atau admin.")
	}
	empType := in.EmploymentType
	if empType == "" {
		empType = member.EmploymentEmployee
	}
	if !empType.IsValid() {
		return member.Member{}, "", errs.Validation("Jenis hubungan kerja harus karyawan atau freelancer.")
	}

	org, err := uc.ws.GetOrganization(ctx, claims.OrganizationID)
	if err != nil {
		return member.Member{}, "", err
	}
	period, err := uc.ws.GetActivePeriod(ctx, claims.OrganizationID)
	if err != nil {
		return member.Member{}, "", err
	}
	existing, err := uc.members.List(ctx, claims.OrganizationID, period.ID, true)
	if err != nil {
		return member.Member{}, "", err
	}
	usedSlugs := make(map[string]struct{}, len(existing))
	for _, em := range existing {
		usedSlugs[em.ID] = struct{}{}
		if strings.EqualFold(em.Email, email) {
			return member.Member{}, "", errs.Conflict("Karyawan dengan email ini sudah terdaftar.")
		}
	}

	weeklyLimit := org.HoursPerWeek
	if in.WeeklyLimit != nil {
		if *in.WeeklyLimit < 0 || *in.WeeklyLimit > 168 {
			return member.Member{}, "", errs.Validation("Jam kerja mingguan harus antara 0 dan 168.")
		}
		weeklyLimit = *in.WeeklyLimit
	}
	if in.WorkHoursOverride != nil && (*in.WorkHoursOverride < 0 || *in.WorkHoursOverride > 168) {
		return member.Member{}, "", errs.Validation("Jam kerja khusus harus antara 0 dan 168.")
	}
	if in.ReserveHoursOverride != nil && (*in.ReserveHoursOverride < 0 || *in.ReserveHoursOverride > 168) {
		return member.Member{}, "", errs.Validation("Cadangan khusus harus antara 0 dan 168.")
	}
	if in.AvailableHours != nil && (*in.AvailableHours < 0 || *in.AvailableHours > 168) {
		return member.Member{}, "", errs.Validation("Ketersediaan periode harus antara 0 dan 168.")
	}

	slug := slugify(name)
	base := slug
	for n := 2; ; n++ {
		if _, dup := usedSlugs[slug]; !dup {
			break
		}
		slug = fmt.Sprintf("%s-%d", base, n)
	}

	isActive := true
	if in.IsActive != nil {
		isActive = *in.IsActive
	}
	division := strings.TrimSpace(in.Division)
	team := strings.TrimSpace(in.Team)

	now := uc.clock.Now().UTC()
	m := member.Member{
		ID:                   slug,
		OrganizationID:       claims.OrganizationID,
		Name:                 name,
		Initials:             initialsOf(name),
		Role:                 roleTitle,
		EmploymentType:       empType,
		Team:                 team,
		Division:             division,
		Email:                email,
		AvailableHours:       in.AvailableHours,
		WeeklyLimit:          weeklyLimit,
		WorkHoursOverride:    in.WorkHoursOverride,
		ReserveHoursOverride: in.ReserveHoursOverride,
		Competencies:         []member.MemberCompetency{},
		Prefers:              []string{},
		Avoids:               []string{},
		LeaveDates:           []string{},
		Access:               access,
		IsActive:             isActive,
		InvitePending:        true,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if !claims.Role.CanAccessHR() {
		// Manager creates regular members with default HR fields
		m.Team = ""
		m.Division = ""
		m.WeeklyLimit = org.HoursPerWeek
		m.WorkHoursOverride = nil
		m.ReserveHoursOverride = nil
		m.AvailableHours = nil
		m.IsActive = true
	}

	rawTok, hashTok, err := uc.tokens.NewOpaqueToken()
	if err != nil {
		return member.Member{}, "", errs.Internal("gagal membuat token undangan", err)
	}
	inv := auth.InvitationToken{
		ID:               uuid.NewString(),
		OrganizationID:   claims.OrganizationID,
		OrganizationName: org.Name,
		MemberID:         m.ID,
		Email:            m.Email,
		Name:             m.Name,
		RoleTitle:        m.Role,
		AccessRole:       access,
		TokenHash:        hashTok,
		InvitedByUserID:  claims.UserID,
		InvitedByName:    claims.Name,
		ExpiresAt:        now.Add(InviteTokenTTL),
		CreatedAt:        now,
	}

	err = uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		if m.Division != "" && uc.hr != nil {
			if err := uc.hr.CreateDivision(txCtx, claims.OrganizationID, m.Division); err != nil {
				return err
			}
		}
		if err := uc.members.Create(txCtx, claims.OrganizationID, period.ID, m); err != nil {
			return err
		}
		return uc.auth.CreateInvitation(txCtx, inv)
	})
	if err != nil {
		return member.Member{}, "", err
	}

	acceptURL := fmt.Sprintf("%s/undangan/%s", uc.baseURL, rawTok)
	_ = uc.mailer.SendInvitation(ctx, m.Email, m.Name, org.Name, claims.Name, acceptURL)
	return m, rawTok, nil
}

func (uc *CatalogUseCase) ResendInvite(ctx context.Context, claims auth.Claims, memberID string) (string, error) {
	org, err := uc.ws.GetOrganization(ctx, claims.OrganizationID)
	if err != nil {
		return "", err
	}
	period, err := uc.ws.GetActivePeriod(ctx, claims.OrganizationID)
	if err != nil {
		return "", err
	}
	m, err := uc.members.Get(ctx, claims.OrganizationID, period.ID, memberID)
	if err != nil {
		return "", err
	}
	if m.UserID != "" {
		return "", errs.Conflict("Karyawan ini sudah bergabung dan memiliki akun aktif.")
	}
	rawTok, hashTok, err := uc.tokens.NewOpaqueToken()
	if err != nil {
		return "", err
	}
	now := uc.clock.Now().UTC()
	inv := auth.InvitationToken{
		ID:               uuid.NewString(),
		OrganizationID:   claims.OrganizationID,
		OrganizationName: org.Name,
		MemberID:         m.ID,
		Email:            m.Email,
		Name:             m.Name,
		RoleTitle:        m.Role,
		AccessRole:       m.Access,
		TokenHash:        hashTok,
		InvitedByUserID:  claims.UserID,
		InvitedByName:    claims.Name,
		ExpiresAt:        now.Add(InviteTokenTTL),
		CreatedAt:        now,
	}
	if err := uc.auth.CreateInvitation(ctx, inv); err != nil {
		return "", err
	}
	acceptURL := fmt.Sprintf("%s/undangan/%s", uc.baseURL, rawTok)
	_ = uc.mailer.SendInvitation(ctx, m.Email, m.Name, org.Name, claims.Name, acceptURL)
	return rawTok, nil
}

func (uc *CatalogUseCase) UpdateMemberProfile(ctx context.Context, claims auth.Claims, id string, in UpdateMemberInput) (member.Member, error) {
	period, err := uc.ws.GetActivePeriod(ctx, claims.OrganizationID)
	if err != nil {
		return member.Member{}, err
	}
	all, err := uc.members.List(ctx, claims.OrganizationID, period.ID, true)
	if err != nil {
		return member.Member{}, err
	}
	var m member.Member
	found := false
	for _, cand := range all {
		if cand.ID == id {
			m = cand
			found = true
			break
		}
	}
	if !found {
		return member.Member{}, errs.NotFound("Karyawan tidak ditemukan.")
	}

	if in.Name != nil && strings.TrimSpace(*in.Name) != "" {
		m.Name = strings.TrimSpace(*in.Name)
		m.Initials = initialsOf(m.Name)
	}
	if in.RoleTitle != nil && strings.TrimSpace(*in.RoleTitle) != "" {
		m.Role = strings.TrimSpace(*in.RoleTitle)
	}
	if in.Email != nil && strings.TrimSpace(*in.Email) != "" {
		nextEmail := strings.ToLower(strings.TrimSpace(*in.Email))
		if !emailRe.MatchString(nextEmail) {
			return member.Member{}, errs.Validation("Email belum valid.")
		}
		for _, other := range all {
			if other.ID != id && strings.EqualFold(other.Email, nextEmail) {
				return member.Member{}, errs.Conflict("Email sudah digunakan anggota lain.")
			}
		}
		m.Email = nextEmail
	}

	// Only HR or Admin may update HR-managed employment/capacity/placement fields.
	// Manager updates to weeklyLimit are explicitly ignored per ENDPOINTS-CORPORATE-FIRST §3.
	if claims.Role.CanAccessHR() {
		if in.EmploymentType != nil && *in.EmploymentType != "" {
			if !in.EmploymentType.IsValid() {
				return member.Member{}, errs.Validation("Jenis hubungan kerja harus karyawan atau freelancer.")
			}
			m.EmploymentType = *in.EmploymentType
		}
		if in.Team != nil {
			m.Team = strings.TrimSpace(*in.Team)
		}
		if in.Division != nil {
			m.Division = strings.TrimSpace(*in.Division)
		}
		if in.WeeklyLimit != nil {
			if *in.WeeklyLimit < 0 || *in.WeeklyLimit > 168 {
				return member.Member{}, errs.Validation("Jam kerja mingguan harus antara 0 dan 168.")
			}
			m.WeeklyLimit = *in.WeeklyLimit
		}
		if in.HasWorkHoursOverride {
			if in.WorkHoursOverride != nil && (*in.WorkHoursOverride < 0 || *in.WorkHoursOverride > 168) {
				return member.Member{}, errs.Validation("Jam kerja khusus harus antara 0 dan 168.")
			}
			m.WorkHoursOverride = in.WorkHoursOverride
		}
		if in.HasReserveHoursOverride {
			if in.ReserveHoursOverride != nil && (*in.ReserveHoursOverride < 0 || *in.ReserveHoursOverride > 168) {
				return member.Member{}, errs.Validation("Cadangan khusus harus antara 0 dan 168.")
			}
			m.ReserveHoursOverride = in.ReserveHoursOverride
		}
		if in.IsActive != nil {
			m.IsActive = *in.IsActive
		}
		if m.EmploymentType != member.EmploymentFreelancer && uc.hr != nil {
			hrSet, _ := uc.hr.GetHRSettings(ctx, claims.OrganizationID)
			workH := hrSet.WeeklyHours
			if m.WorkHoursOverride != nil {
				workH = *m.WorkHoursOverride
			}
			resH := hrSet.ReserveHours
			if m.ReserveHoursOverride != nil {
				resH = *m.ReserveHoursOverride
			}
			if resH > workH {
				return member.Member{}, errs.Validation("Cadangan tidak boleh melebihi jam kerja.")
			}
		}
	}

	m.UpdatedAt = uc.clock.Now().UTC()
	err = uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		if claims.Role.CanAccessHR() && m.Division != "" && uc.hr != nil {
			if err := uc.hr.CreateDivision(txCtx, claims.OrganizationID, m.Division); err != nil {
				return err
			}
		}
		if err := uc.members.UpdateProfile(txCtx, claims.OrganizationID, m); err != nil {
			return err
		}
		if claims.Role.CanAccessHR() && in.HasAvailableHours {
			if in.AvailableHours != nil && (*in.AvailableHours < 0 || *in.AvailableHours > 168) {
				return errs.Validation("Ketersediaan harus antara 0 dan 168.")
			}
			if err := uc.members.SaveAvailability(txCtx, claims.OrganizationID, period.ID, id, in.AvailableHours, m.WeeklyLimit, m.LeaveDates); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return member.Member{}, err
	}
	return uc.members.Get(ctx, claims.OrganizationID, period.ID, id)
}

func (uc *CatalogUseCase) SaveMemberAvailability(ctx context.Context, orgID, periodID, memberID string, avail *float64, limit float64, leaveDates []string) (member.Member, error) {
	if avail != nil && (*avail < 0 || *avail > 168) {
		return member.Member{}, errs.Validation("Jam tersedia antara 0 dan 168.")
	}
	if limit < 0 || limit > 168 {
		return member.Member{}, errs.Validation("Batas kerja mingguan antara 0 dan 168.")
	}
	if periodID == "" {
		p, err := uc.ws.GetActivePeriod(ctx, orgID)
		if err != nil {
			return member.Member{}, err
		}
		periodID = p.ID
	}
	if _, err := uc.members.Get(ctx, orgID, periodID, memberID); err != nil {
		return member.Member{}, err
	}
	if leaveDates == nil {
		leaveDates = []string{}
	}
	if err := uc.members.SaveAvailability(ctx, orgID, periodID, memberID, avail, limit, leaveDates); err != nil {
		return member.Member{}, err
	}
	return uc.members.Get(ctx, orgID, periodID, memberID)
}

// DecideMemberLeaveByManager records the manager's parallel decision on a member's leave request.
func (uc *CatalogUseCase) DecideMemberLeaveByManager(ctx context.Context, claims auth.Claims, memberID, reqID string, approve bool) (member.Member, error) {
	now := uc.clock.Now().UTC()
	var updated member.Member
	var decidedReq *member.LeaveRequest
	var memberNotice *notification.Notice

	err := uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		period, err := uc.ws.GetActivePeriod(txCtx, claims.OrganizationID)
		if err != nil {
			return err
		}
		m, err := uc.members.Get(txCtx, claims.OrganizationID, period.ID, memberID)
		if err != nil {
			return err
		}
		next, req, ok := engine.DecideLeave(m, reqID, "manager", approve)
		if !ok {
			return errs.Conflict("Pengajuan cuti tidak ditemukan atau sudah diputuskan.")
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
			msg := "ditolak manajer"
			if approve {
				if req.CombinedStatus() == member.ApprovalApproved {
					msg = "disetujui penuh (manajer & HR)"
				} else {
					msg = "disetujui manajer, menunggu HR"
				}
			}
			memberNotice = &notification.Notice{
				ID:             fmt.Sprintf("cuti-mgr-%s", req.ID),
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

// ImportMembersCSV validates (dryRun=true) or imports (dryRun=false) members from CSV (7 columns).
func (uc *CatalogUseCase) ImportMembersCSV(ctx context.Context, claims auth.Claims, csvText string, dryRun bool) ([]ImportMemberRow, error) {
	raw := strings.TrimSpace(csvText)
	if raw == "" {
		return nil, errs.Validation("Data CSV tidak boleh kosong.")
	}
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.ToLower(strings.TrimSpace(lines[0])) != "nama,email,jabatan,jenis,tim,divisi,jam" {
		return nil, errs.Validation("Header harus: nama,email,jabatan,jenis,tim,divisi,jam.")
	}

	period, err := uc.ws.GetActivePeriod(ctx, claims.OrganizationID)
	if err != nil {
		return nil, err
	}
	existing, err := uc.members.List(ctx, claims.OrganizationID, period.ID, true)
	if err != nil {
		return nil, err
	}
	usedSlugs := make(map[string]bool, len(existing))
	usedEmails := make(map[string]bool, len(existing))
	for _, em := range existing {
		usedSlugs[em.ID] = true
		usedEmails[strings.ToLower(em.Email)] = true
	}

	now := uc.clock.Now().UTC()
	rows := make([]ImportMemberRow, 0)
	var validMembers []member.Member
	var validIndices []int

	for idx, rawLine := range lines[1:] {
		if strings.TrimSpace(rawLine) == "" {
			continue
		}
		lineNum := idx + 2
		parts := strings.Split(rawLine, ",")
		if len(parts) != 7 {
			rows = append(rows, ImportMemberRow{Line: lineNum, Error: "Jumlah kolom harus 7: nama,email,jabatan,jenis,tim,divisi,jam"})
			continue
		}
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		name, emailRaw, role, empStr, team, division, hrsStr := parts[0], parts[1], parts[2], parts[3], parts[4], parts[5], parts[6]
		email := strings.ToLower(emailRaw)
		empType := member.EmploymentType(empStr)
		hrsVal, hErr := strconv.ParseFloat(hrsStr, 64)

		if name == "" || role == "" {
			rows = append(rows, ImportMemberRow{Line: lineNum, Error: "Nama dan jabatan wajib diisi"})
			continue
		}
		if !emailRe.MatchString(email) {
			rows = append(rows, ImportMemberRow{Line: lineNum, Error: fmt.Sprintf("Email %q tidak valid", emailRaw)})
			continue
		}
		if usedEmails[email] {
			rows = append(rows, ImportMemberRow{Line: lineNum, Error: fmt.Sprintf("Email %q sudah digunakan", email)})
			continue
		}
		if !empType.IsValid() {
			rows = append(rows, ImportMemberRow{Line: lineNum, Error: fmt.Sprintf("Jenis %q harus karyawan atau freelancer", empStr)})
			continue
		}
		if hrsStr == "" || hErr != nil || hrsVal < 0 || hrsVal > 168 {
			rows = append(rows, ImportMemberRow{Line: lineNum, Error: "Jam kerja harus angka antara 0 dan 168"})
			continue
		}

		slug := slugify(name)
		base := slug
		for n := 2; ; n++ {
			if !usedSlugs[slug] {
				break
			}
			slug = fmt.Sprintf("%s-%d", base, n)
		}
		usedSlugs[slug] = true
		usedEmails[email] = true

		m := member.Member{
			ID:             slug,
			OrganizationID: claims.OrganizationID,
			Name:           name,
			Initials:       initialsOf(name),
			Role:           role,
			EmploymentType: empType,
			Team:           team,
			Division:       division,
			Email:          email,
			WeeklyLimit:    hrsVal,
			Competencies:   []member.MemberCompetency{},
			Prefers:        []string{},
			Avoids:         []string{},
			LeaveDates:     []string{},
			Access:         auth.RoleMember,
			IsActive:       true,
			InvitePending:  true,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		cp := m
		rows = append(rows, ImportMemberRow{Line: lineNum, Member: &cp})
		validMembers = append(validMembers, m)
		validIndices = append(validIndices, len(rows)-1)
	}

	if dryRun || len(validMembers) == 0 {
		return rows, nil
	}

	err = uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		for i, m := range validMembers {
			if m.Division != "" && uc.hr != nil {
				if err := uc.hr.CreateDivision(txCtx, claims.OrganizationID, m.Division); err != nil {
					return err
				}
			}
			if err := uc.members.Create(txCtx, claims.OrganizationID, period.ID, m); err != nil {
				return err
			}
			cp := m
			rows[validIndices[i]].Member = &cp
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (uc *CatalogUseCase) VerifyCompetency(ctx context.Context, claims auth.Claims, memberID, compID string) (member.Member, error) {
	p, err := uc.ws.GetActivePeriod(ctx, claims.OrganizationID)
	if err != nil {
		return member.Member{}, err
	}
	if err := uc.members.VerifyCompetency(ctx, claims.OrganizationID, memberID, compID, claims.UserID, uc.clock.Now().UTC()); err != nil {
		return member.Member{}, err
	}
	return uc.members.Get(ctx, claims.OrganizationID, p.ID, memberID)
}

func (uc *CatalogUseCase) SetMemberAccessAndActive(ctx context.Context, claims auth.Claims, memberID string, access *auth.Role, active *bool) (member.Member, error) {
	p, err := uc.ws.GetActivePeriod(ctx, claims.OrganizationID)
	if err != nil {
		return member.Member{}, err
	}
	m, err := uc.members.Get(ctx, claims.OrganizationID, p.ID, memberID)
	if err != nil {
		return member.Member{}, err
	}
	if claims.MemberID == memberID && active != nil && !*active {
		return member.Member{}, errs.Validation("Tidak bisa menonaktifkan akun sendiri.")
	}
	targetRole := m.Access
	if access != nil {
		if !access.IsValid() {
			return member.Member{}, errs.Validation("Peran akses tidak dikenal.")
		}
		targetRole = *access
	}
	targetActive := m.IsActive
	if active != nil {
		targetActive = *active
	}

	err = uc.tx.WithinTx(ctx, func(txCtx context.Context) error {
		if err := uc.members.SetAccess(txCtx, claims.OrganizationID, memberID, targetRole); err != nil {
			return err
		}
		if err := uc.members.SetActive(txCtx, claims.OrganizationID, memberID, targetActive); err != nil {
			return err
		}
		if m.UserID != "" {
			if err := uc.auth.UpdateUserAccessAndActive(txCtx, claims.OrganizationID, memberID, targetRole, targetActive, !targetActive); err != nil {
				return err
			}
			if !targetActive {
				_ = uc.auth.RevokeAllRefreshSessions(txCtx, m.UserID)
			}
		}
		return nil
	})
	if err != nil {
		return member.Member{}, err
	}
	if m.UserID != "" {
		if u, uErr := uc.auth.GetUserByID(ctx, m.UserID); uErr == nil {
			_ = uc.sec.SetSessionVersion(ctx, u.ID, u.SessionVersion, AccessTokenTTL)
		}
	}
	return uc.members.Get(ctx, claims.OrganizationID, p.ID, memberID)
}

// RemindMember sends a persisted reminder notification to an employee missing period availability.
func (uc *CatalogUseCase) RemindMember(ctx context.Context, claims auth.Claims, memberID string) error {
	p, err := uc.ws.GetActivePeriod(ctx, claims.OrganizationID)
	if err != nil {
		return err
	}
	if _, err := uc.members.Get(ctx, claims.OrganizationID, p.ID, memberID); err != nil {
		return err
	}
	u, err := uc.auth.GetUserByMemberID(ctx, claims.OrganizationID, memberID)
	if err != nil {
		// Member hasn't accepted invite yet; re-sending notification safely no-ops if no user account exists yet.
		return nil
	}
	n := notification.Notice{
		ID:             fmt.Sprintf("remind-%s-%s", p.ID, memberID),
		OrganizationID: claims.OrganizationID,
		UserID:         u.ID,
		Title:          "Lengkapi ketersediaan periode " + p.Label,
		Subtitle:       claims.Name + " mengingatkanmu untuk mengisi jam tersedia supaya bisa ikut penugasan.",
		Href:           "/saya/ketersediaan",
		Kind:           notification.KindReminder,
		CreatedAt:      uc.clock.Now().UTC(),
	}
	return uc.notifs.Upsert(ctx, n)
}
