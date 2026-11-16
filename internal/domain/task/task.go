package task

import (
	"time"

	"github.com/Orang2-BU/pantara-back/internal/domain/member"
)

// Priority indicates relative scheduling urgency.
type Priority string

const (
	PriorityLow    Priority = "rendah"
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "tinggi"
)

func (p Priority) IsValid() bool {
	return p == PriorityLow || p == PriorityNormal || p == PriorityHigh
}

// Status represents the lifecycle state of a task.
type Status string

const (
	StatusDraft    Status = "draf"
	StatusOpen     Status = "belum"
	StatusWaiting  Status = "menunggu"
	StatusRunning  Status = "berjalan"
	StatusDone     Status = "selesai"
)

func (s Status) IsValid() bool {
	switch s {
	case StatusDraft, StatusOpen, StatusWaiting, StatusRunning, StatusDone:
		return true
	default:
		return false
	}
}

// IsActive reports whether the task occupies active member capacity.
func (s Status) IsActive() bool {
	return s == StatusWaiting || s == StatusRunning
}

// EffortSize represents T-shirt effort tiers: S <= 4h, M 4-8h, L 8-16h, XL > 16h.
type EffortSize string

const (
	EffortS  EffortSize = "S"
	EffortM  EffortSize = "M"
	EffortL  EffortSize = "L"
	EffortXL EffortSize = "XL"
)

func (e EffortSize) IsValid() bool {
	return e == EffortS || e == EffortM || e == EffortL || e == EffortXL
}

// Requirement defines one decomposed skill demand with multi-sample confidence p.
type Requirement struct {
	CompetencyID string       `json:"competencyId"`
	Level        member.Level `json:"level"`
	P            float64      `json:"p"`
	Quote        string       `json:"quote"`
}

// Dependency links a prerequisite task with multi-sample confidence p.
type Dependency struct {
	TaskID string  `json:"taskId"`
	P      float64 `json:"p"`
}

// TaskDNA holds AI-decomposed and human-confirmed structured requirements.
type TaskDNA struct {
	Requirements     []Requirement `json:"requirements"`
	EffortSize       EffortSize    `json:"effortSize"`
	EffortP          float64       `json:"effortP"`
	EffortHours      float64       `json:"effortHours"`
	Complexity       member.Level  `json:"complexity"`
	ComplexitySpread int           `json:"complexitySpread"`
	Dependencies     []Dependency  `json:"dependencies"`
	Model            string        `json:"model"`
	Prompt           string        `json:"prompt"`
	InputHash        string        `json:"inputHash,omitempty"`
	Confirmed        bool          `json:"confirmed"`
	ConfirmedAt      *time.Time    `json:"confirmedAt,omitempty"`
}

// Assignment records committed effort hours per assigned member on a task.
type Assignment struct {
	MemberID string  `json:"memberId"`
	Hours    float64 `json:"hours"`
}

// Task represents an operational work item inside a project and period.
type Task struct {
	ID             string       `json:"id"`
	OrganizationID string       `json:"organizationId,omitempty"`
	ProjectID      string       `json:"projectId"`
	PeriodID       string       `json:"periodId,omitempty"`
	Title          string       `json:"title"`
	Description    string       `json:"description"`
	DueDate        string       `json:"dueDate"`
	Priority       Priority     `json:"priority"`
	Status         Status       `json:"status"`
	Assignments    []Assignment `json:"assignments"`
	RemainingHours float64      `json:"remainingHours"`
	CreatedAt      string       `json:"createdAt"`
	DNA            *TaskDNA     `json:"dna,omitempty"`
	CompetencyIDs  []string     `json:"competencyIds"`
	ResponseReason string       `json:"responseReason,omitempty"`
	ActualHours    *float64     `json:"actualHours,omitempty"`
	FlowStartedAt  *time.Time   `json:"flowStartedAt,omitempty"`
	IsArchived     bool         `json:"isArchived,omitempty"`
	CreatedBy      string       `json:"createdBy,omitempty"`
	UpdatedAt      time.Time    `json:"updatedAt,omitempty"`
}

// EffortOrRemaining returns DNA effort hours when set, otherwise remaining hours.
func (t Task) EffortOrRemaining() float64 {
	if t.DNA != nil && t.DNA.EffortHours > 0 {
		return t.DNA.EffortHours
	}
	return t.RemainingHours
}
