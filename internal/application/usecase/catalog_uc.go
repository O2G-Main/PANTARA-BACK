package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/Orang2-BU/pantara-back/internal/application/port"
	"github.com/Orang2-BU/pantara-back/internal/domain/auth"
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

// CreateAndInviteMember registers a new team member and issues a single-use invitation token.
func (uc *CatalogUseCase) CreateAndInviteMember(ctx context.Context, claims auth.Claims, name, email, roleTitle string, access auth.Role) (member.Member, string, error) {
	name = strings.TrimSpace(name)
	email = strings.ToLower(strings.TrimSpace(email))
	roleTitle = strings.TrimSpace(roleTitle)
	if len(name) < 3 {
		return member.Member{}, "", errs.Validation("Tulis nama lengkap karyawan.")
	}
	if !emailRe.MatchString(email) {
		return member.Member{}, "", errs.Validation("Email belum valid.")
	}
	if roleTitle == "" {
		roleTitle = "Pengembang perangkat lunak"
	}
	if !access.IsValid() {
		access = auth.RoleMember
	}
	if access != auth.RoleMember && !claims.Role.CanAdminister() {
		return member.Member{}, "", errs.Forbidden("Hanya admin yang dapat menetapkan peran manajer atau admin.")
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

	slug := slugify(name)
	base := slug
	for n := 2; ; n++ {
		if _, dup := usedSlugs[slug]; !dup {
			break
		}
		slug = fmt.Sprintf("%s-%d", base, n)
	}

	now := uc.clock.Now().UTC()
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
		Access:         access,
		IsActive:       true,
		InvitePending:  true,
		CreatedAt:      now,
		UpdatedAt:      now,
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

func (uc *CatalogUseCase) UpdateMemberProfile(ctx context.Context, orgID, id, name, roleTitle, email string, weeklyLimit float64) (member.Member, error) {
	period, err := uc.ws.GetActivePeriod(ctx, orgID)
	if err != nil {
		return member.Member{}, err
	}
	m, err := uc.members.Get(ctx, orgID, period.ID, id)
	if err != nil {
		return member.Member{}, err
	}
	if strings.TrimSpace(name) != "" {
		m.Name = strings.TrimSpace(name)
		m.Initials = initialsOf(m.Name)
	}
	if strings.TrimSpace(roleTitle) != "" {
		m.Role = strings.TrimSpace(roleTitle)
	}
	if strings.TrimSpace(email) != "" && emailRe.MatchString(email) {
		m.Email = strings.ToLower(strings.TrimSpace(email))
	}
	if weeklyLimit >= 1 && weeklyLimit <= 80 {
		m.WeeklyLimit = weeklyLimit
	}
	m.UpdatedAt = uc.clock.Now().UTC()
	if err := uc.members.UpdateProfile(ctx, orgID, m); err != nil {
		return member.Member{}, err
	}
	return uc.members.Get(ctx, orgID, period.ID, id)
}

func (uc *CatalogUseCase) SaveMemberAvailability(ctx context.Context, orgID, periodID, memberID string, avail *float64, limit float64, leaveDates []string) (member.Member, error) {
	if avail != nil && (*avail < 0 || *avail > 60) {
		return member.Member{}, errs.Validation("Jam tersedia antara 0 dan 60.")
	}
	if avail != nil && limit < *avail {
		return member.Member{}, errs.Validation("Batas kerja tidak boleh lebih kecil dari jam tersedia.")
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
