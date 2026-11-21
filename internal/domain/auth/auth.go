package auth

import "time"

// Role defines authorization access within an organization workspace.
type Role string

const (
	RoleAdmin   Role = "admin"
	RoleManager Role = "manajer"
	RoleMember  Role = "anggota"
	RoleHR      Role = "hr"
)

// IsValid checks whether the role is one of the supported canonical values.
func (r Role) IsValid() bool {
	return r == RoleAdmin || r == RoleManager || r == RoleMember || r == RoleHR
}

// CanManage reports whether the role has operational manager privileges.
func (r Role) CanManage() bool {
	return r == RoleAdmin || r == RoleManager
}

// CanAdminister reports whether the role can change org-wide settings, users, and billing.
func (r Role) CanAdminister() bool {
	return r == RoleAdmin
}

// IsHR reports whether the role is the dedicated HR workspace role.
func (r Role) IsHR() bool {
	return r == RoleHR
}

// CanAccessHR reports whether the role can administer HR data/settings alongside admin.
func (r Role) CanAccessHR() bool {
	return r == RoleHR || r == RoleAdmin
}

// HomePath returns the default frontend landing route for the user role.
func (r Role) HomePath() string {
	if r == RoleHR {
		return "/hr"
	}
	if r.CanManage() {
		return "/dashboard"
	}
	return "/saya"
}

// NotifyPrefs stores user-configurable notification delivery toggles.
type NotifyPrefs struct {
	Assigned bool `json:"assigned"`
	Rejected bool `json:"rejected"`
	Reminder bool `json:"reminder"`
}

// DefaultNotifyPrefs provides safe defaults matching frontend P33 settings.
func DefaultNotifyPrefs() NotifyPrefs {
	return NotifyPrefs{Assigned: true, Rejected: true, Reminder: false}
}

// User represents an authenticated account scoped to one organization.
type User struct {
	ID             string
	OrganizationID string
	MemberID       string
	Email          string
	Name           string
	PasswordHash   string
	Role           Role
	IsActive       bool
	SessionVersion int64
	NotifyPrefs    NotifyPrefs
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Claims represents validated identity data extracted from an access token.
type Claims struct {
	UserID         string
	OrganizationID string
	MemberID       string
	Email          string
	Name           string
	Role           Role
	SessionVersion int64
}

// RefreshSession tracks active refresh tokens for rotation and device revocation.
type RefreshSession struct {
	ID         string
	UserID     string
	TokenHash  string
	RememberMe bool
	UserAgent  string
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	CreatedAt  time.Time
}

// InvitationToken represents an active single-use workspace invite link.
type InvitationToken struct {
	ID               string
	OrganizationID   string
	OrganizationName string
	MemberID         string
	Email            string
	Name             string
	RoleTitle        string
	AccessRole       Role
	TokenHash        string
	InvitedByUserID  string
	InvitedByName    string
	ExpiresAt        time.Time
	AcceptedAt       *time.Time
	CreatedAt        time.Time
}

// PasswordResetToken represents a single-use password reset link valid for 30 minutes.
type PasswordResetToken struct {
	ID        string
	UserID    string
	Email     string
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}
