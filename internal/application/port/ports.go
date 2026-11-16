package port

import (
	"context"
	"time"

	"github.com/Orang2-BU/pantara-back/internal/domain/auth"
	"github.com/Orang2-BU/pantara-back/internal/domain/decision"
	"github.com/Orang2-BU/pantara-back/internal/domain/member"
	"github.com/Orang2-BU/pantara-back/internal/domain/notification"
	"github.com/Orang2-BU/pantara-back/internal/domain/project"
	"github.com/Orang2-BU/pantara-back/internal/domain/task"
	"github.com/Orang2-BU/pantara-back/internal/domain/workspace"
)

// TxManager wraps a unit of work in an atomic database transaction.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Clock abstracts time for deterministic unit testing and relative demo seeding.
type Clock interface {
	Now() time.Time
}

// PasswordHasher hashes and verifies user passwords cleanly.
type PasswordHasher interface {
	Hash(plain string) (string, error)
	Verify(plain, hash string) bool
}

// TokenService issues signed access JWTs and cryptographically random opaque tokens.
type TokenService interface {
	IssueAccessToken(claims auth.Claims, ttl time.Duration) (string, error)
	VerifyAccessToken(tokenStr string) (auth.Claims, error)
	NewOpaqueToken() (raw string, hash string, err error)
	HashOpaqueToken(raw string) string
}

// Mailer delivers invitation and password reset emails (logs safely in dev).
type Mailer interface {
	SendInvitation(ctx context.Context, toEmail, toName, orgName, inviterName, acceptURL string) error
	SendPasswordReset(ctx context.Context, toEmail, resetURL string) error
}

// TaskDNAnalyzer decomposes task title & description into structured TaskDNA.
type TaskDNAnalyzer interface {
	Analyze(ctx context.Context, title, description string, priority task.Priority, catalog []member.Competency, tasks []task.Task) (task.TaskDNA, error)
}

// SecurityStore manages rate limits, login lockouts, session versions, and TaskDNA cache.
type SecurityStore interface {
	AllowRate(ctx context.Context, key string, limit int, window time.Duration) (bool, error)
	GetLoginLock(ctx context.Context, key string) (bool, time.Duration, error)
	RecordLoginFailure(ctx context.Context, key string, maxTries int, lockTTL time.Duration) (int, time.Time, error)
	ClearLoginFailures(ctx context.Context, key string) error
	GetSessionVersion(ctx context.Context, userID string) (int64, bool, error)
	SetSessionVersion(ctx context.Context, userID string, version int64, ttl time.Duration) error
	GetCachedDNA(ctx context.Context, hash string) (*task.TaskDNA, bool, error)
	SetCachedDNA(ctx context.Context, hash string, dna task.TaskDNA, ttl time.Duration) error
}

// AuthRepository persists users, refresh sessions, invitations, and password resets.
type AuthRepository interface {
	CreateWorkspaceBootstrap(
		ctx context.Context,
		org workspace.Organization,
		period workspace.WorkPeriod,
		settings workspace.EngineSettings,
		competencies []member.Competency,
		adminUser auth.User,
	) error
	GetUserByEmail(ctx context.Context, email string) (auth.User, error)
	GetUserByID(ctx context.Context, userID string) (auth.User, error)
	GetUserByMemberID(ctx context.Context, orgID, memberID string) (auth.User, error)
	ListUsersByOrg(ctx context.Context, orgID string) ([]auth.User, error)
	UpdateUserProfile(ctx context.Context, userID, name, email string) error
	UpdateUserPassword(ctx context.Context, userID, passwordHash string, bumpSession bool) error
	UpdateUserNotifyPrefs(ctx context.Context, userID string, prefs auth.NotifyPrefs) error
	UpdateUserAccessAndActive(ctx context.Context, orgID, memberID string, role auth.Role, active bool, bumpSession bool) error
	BumpSessionVersion(ctx context.Context, userID string) (int64, error)
	CreateRefreshSession(ctx context.Context, s auth.RefreshSession) error
	GetRefreshSessionByHash(ctx context.Context, tokenHash string) (auth.RefreshSession, error)
	RevokeRefreshSession(ctx context.Context, id string) error
	RevokeAllRefreshSessions(ctx context.Context, userID string) error
	CreateInvitation(ctx context.Context, inv auth.InvitationToken) error
	GetInvitationByHash(ctx context.Context, tokenHash string) (auth.InvitationToken, error)
	ListPendingInvitations(ctx context.Context, orgID string) ([]auth.InvitationToken, error)
	AcceptInvitation(ctx context.Context, invitationID string, newUser auth.User, memberID string) error
	CreatePasswordReset(ctx context.Context, reset auth.PasswordResetToken) error
	GetPasswordResetByHash(ctx context.Context, tokenHash string) (auth.PasswordResetToken, error)
	ConsumePasswordReset(ctx context.Context, resetID, userID, newPasswordHash string) error
}

// WorkspaceRepository persists organization configuration, periods, and engine weights.
type WorkspaceRepository interface {
	GetOrganization(ctx context.Context, orgID string) (workspace.Organization, error)
	UpdateOrganization(ctx context.Context, org workspace.Organization) error
	UpdateBaselineMinutes(ctx context.Context, orgID string, minutes float64) error
	ListPeriods(ctx context.Context, orgID string) ([]workspace.WorkPeriod, error)
	GetActivePeriod(ctx context.Context, orgID string) (workspace.WorkPeriod, error)
	GetPeriod(ctx context.Context, orgID, periodID string) (workspace.WorkPeriod, error)
	GetPreviousPeriod(ctx context.Context, orgID, currentPeriodID string) (*workspace.WorkPeriod, error)
	CreatePeriod(ctx context.Context, p workspace.WorkPeriod, activate bool) error
	ActivatePeriod(ctx context.Context, orgID, periodID string) error
	GetEngineSettings(ctx context.Context, orgID string) (workspace.EngineSettings, error)
	SaveEngineSettings(ctx context.Context, orgID string, s workspace.EngineSettings) error
}

// CompetencyRepository persists organization skill catalogs.
type CompetencyRepository interface {
	List(ctx context.Context, orgID string, includeArchived bool) ([]member.Competency, error)
	Get(ctx context.Context, orgID, id string) (member.Competency, error)
	Create(ctx context.Context, c member.Competency) error
	Update(ctx context.Context, c member.Competency) error
	ArchiveOrDelete(ctx context.Context, orgID, id string) error
}

// MemberRepository persists member profiles, competencies, preferences, and period availability.
type MemberRepository interface {
	List(ctx context.Context, orgID, periodID string, includeInactive bool) ([]member.Member, error)
	Get(ctx context.Context, orgID, periodID, id string) (member.Member, error)
	GetAvailability(ctx context.Context, memberID, periodID string) (*member.AvailabilitySnapshot, error)
	Create(ctx context.Context, orgID, periodID string, m member.Member) error
	UpdateProfile(ctx context.Context, orgID string, m member.Member) error
	SaveAvailability(ctx context.Context, orgID, periodID, memberID string, avail *float64, limit float64, leaveDates []string) error
	VerifyCompetency(ctx context.Context, orgID, memberID, compID, verifierID string, at time.Time) error
	SetActive(ctx context.Context, orgID, memberID string, active bool) error
	SetAccess(ctx context.Context, orgID, memberID string, role auth.Role) error
}

// ProjectRepository persists projects scoped to an organization.
type ProjectRepository interface {
	List(ctx context.Context, orgID string, includeArchived bool) ([]project.Project, error)
	Get(ctx context.Context, orgID, id string) (project.Project, error)
	Create(ctx context.Context, p project.Project) error
	Update(ctx context.Context, p project.Project) error
	ArchiveOrDelete(ctx context.Context, orgID, id string) error
}

// TaskRepository persists tasks, TaskDNAs, and assignments.
type TaskRepository interface {
	List(ctx context.Context, orgID, periodID string, includeArchived bool) ([]task.Task, error)
	Get(ctx context.Context, orgID, id string) (task.Task, error)
	GetForUpdate(ctx context.Context, orgID, id string) (task.Task, error)
	NextTaskCode(ctx context.Context, orgID string) (string, error)
	Create(ctx context.Context, t task.Task) error
	Update(ctx context.Context, t task.Task) error
	DeleteOrArchive(ctx context.Context, orgID, id string) error
	SetFlowStartedIfEmpty(ctx context.Context, orgID, id string, at time.Time) error
}

// DecisionRepository appends and reads immutable decision audit entries.
type DecisionRepository interface {
	List(ctx context.Context, orgID string) ([]decision.Decision, error)
	ListByTask(ctx context.Context, orgID, taskID string) ([]decision.Decision, error)
	NextDecisionCode(ctx context.Context, orgID string) (string, error)
	Create(ctx context.Context, d decision.Decision) error
}

// NotificationRepository stores user notifications and read acknowledgments.
type NotificationRepository interface {
	ListByUser(ctx context.Context, orgID, userID string) ([]notification.Notice, error)
	Upsert(ctx context.Context, n notification.Notice) error
	MarkRead(ctx context.Context, orgID, userID, id string, at time.Time) error
	MarkAllRead(ctx context.Context, orgID, userID string, at time.Time) error
}
