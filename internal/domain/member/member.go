package member

import (
	"time"

	"github.com/Orang2-BU/pantara-back/internal/domain/auth"
)

// Level represents competency mastery on a 0..3 scale (Belum, Dasar, Mandiri, Mahir).
type Level int

const (
	LevelNone    Level = 0
	LevelBasic   Level = 1
	LevelIndep   Level = 2
	LevelExpert  Level = 3
)

// CompetencySource distinguishes how a competency level was established.
type CompetencySource string

const (
	SourceClaim    CompetencySource = "klaim"
	SourceVerified CompetencySource = "verifikasi"
	SourceHistory  CompetencySource = "riwayat"
)

// IsValid reports whether s is a valid competency source.
func (s CompetencySource) IsValid() bool {
	return s == SourceClaim || s == SourceVerified || s == SourceHistory
}

// Competency represents a workspace-wide skill catalog entry.
type Competency struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organizationId,omitempty"`
	Name           string    `json:"name"`
	Category       string    `json:"category"`
	IsArchived     bool      `json:"isArchived,omitempty"`
	CreatedAt      time.Time `json:"createdAt,omitempty"`
}

// MemberCompetency links a member to a competency with level and verification source.
type MemberCompetency struct {
	ID         string           `json:"id"`
	Level      Level            `json:"level"`
	Source     CompetencySource `json:"source"`
	VerifiedBy string           `json:"verifiedBy,omitempty"`
	VerifiedAt *time.Time       `json:"verifiedAt,omitempty"`
}

// EmploymentType distinguishes regular employees from freelancers.
type EmploymentType string

const (
	EmploymentEmployee   EmploymentType = "karyawan"
	EmploymentFreelancer EmploymentType = "freelancer"
)

// IsValid reports whether e is a supported employment type.
func (e EmploymentType) IsValid() bool {
	return e == EmploymentEmployee || e == EmploymentFreelancer
}

// LeaveType classifies employee leave requests.
type LeaveType string

const (
	LeaveAnnual LeaveType = "tahunan"
	LeaveSick   LeaveType = "sakit"
	LeavePermit LeaveType = "izin"
)

// IsValid reports whether t is a supported leave type.
func (t LeaveType) IsValid() bool {
	return t == LeaveAnnual || t == LeaveSick || t == LeavePermit
}

// ApprovalStatus tracks parallel manager/HR approval states.
type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "menunggu"
	ApprovalApproved ApprovalStatus = "disetujui"
	ApprovalRejected ApprovalStatus = "ditolak"
)

// LeaveRequest represents a member's leave request approved in parallel by manager and HR.
type LeaveRequest struct {
	ID          string         `json:"id"`
	MemberID    string         `json:"memberId,omitempty"`
	Start       string         `json:"start"`
	End         string         `json:"end"`
	Type        LeaveType      `json:"type"`
	Reason      string         `json:"reason"`
	RequestedAt string         `json:"requestedAt"`
	Manager     ApprovalStatus `json:"manager"`
	HR          ApprovalStatus `json:"hr"`
}

// CombinedStatus computes the overall decision status from manager and HR approvals.
func (r LeaveRequest) CombinedStatus() ApprovalStatus {
	if r.Manager == ApprovalRejected || r.HR == ApprovalRejected {
		return ApprovalRejected
	}
	if r.Manager == ApprovalApproved && r.HR == ApprovalApproved {
		return ApprovalApproved
	}
	return ApprovalPending
}

// Member represents a team employee profile, including period-scoped availability.
type Member struct {
	ID                   string             `json:"id"`
	OrganizationID       string             `json:"organizationId,omitempty"`
	UserID               string             `json:"userId,omitempty"`
	Name                 string             `json:"name"`
	Initials             string             `json:"initials"`
	Role                 string             `json:"role"`
	EmploymentType       EmploymentType     `json:"employmentType,omitempty"`
	Team                 string             `json:"team,omitempty"`
	Division             string             `json:"division,omitempty"`
	Email                string             `json:"email"`
	AvailableHours       *float64           `json:"availableHours"`
	LeaveDates           []string           `json:"leaveDates"`
	WeeklyLimit          float64            `json:"weeklyLimit"`
	WorkHoursOverride    *float64           `json:"workHoursOverride,omitempty"`
	ReserveHoursOverride *float64           `json:"reserveHoursOverride,omitempty"`
	Competencies         []MemberCompetency `json:"competencies"`
	Prefers              []string           `json:"prefers"`
	Avoids               []string           `json:"avoids"`
	Photo                string             `json:"photo,omitempty"`
	Access               auth.Role          `json:"access"`
	IsActive             bool               `json:"isActive"`
	InvitePending        bool               `json:"invitePending,omitempty"`
	LeaveRequests        []LeaveRequest     `json:"leaveRequests,omitempty"`
	CreatedAt            time.Time          `json:"createdAt,omitempty"`
	UpdatedAt            time.Time          `json:"updatedAt,omitempty"`
}

// AvailabilitySnapshot holds period-scoped capacity hours and leave dates for a member.
type AvailabilitySnapshot struct {
	MemberID       string   `json:"memberId"`
	PeriodID       string   `json:"periodId"`
	AvailableHours *float64 `json:"availableHours"`
	WeeklyLimit    float64  `json:"weeklyLimit"`
	LeaveDates     []string `json:"leaveDates"`
}
