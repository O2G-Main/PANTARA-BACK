package workspace

import "time"

// PresetID enumerates the four recommendation weight presets supported by the scoring engine.
type PresetID string

const (
	PresetSeimbang PresetID = "seimbang"
	PresetTenggat  PresetID = "tenggat"
	PresetRatakan  PresetID = "ratakan"
	PresetSebar    PresetID = "sebar"
)

// IsValid reports whether p is a known recommendation preset.
func (p PresetID) IsValid() bool {
	switch p {
	case PresetSeimbang, PresetTenggat, PresetRatakan, PresetSebar:
		return true
	default:
		return false
	}
}

// PeriodType configures weekly or biweekly capacity periods.
type PeriodType string

const (
	PeriodWeekly   PeriodType = "mingguan"
	PeriodBiweekly PeriodType = "dua-mingguan"
)

// Organization holds tenant-level workspace configuration and baseline metrics.
type Organization struct {
	ID               string     `json:"id"`
	Slug             string     `json:"slug"`
	Name             string     `json:"name"`
	Timezone         string     `json:"timezone"`
	PeriodType       PeriodType `json:"periodType"`
	HoursPerWeek     float64    `json:"hoursPerWeek"`
	WorkDays         []string   `json:"workDays"`
	Holidays         []string   `json:"holidays"`
	BaselineMinutes  float64    `json:"baselineMinutes"`
	SubscriptionPlan string     `json:"subscriptionPlan"`
	OnboardingDone   bool       `json:"onboardingDone"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
}

// WorkPeriod defines a capacity planning window (e.g. 28 Sep - 2 Okt 2026).
type WorkPeriod struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organizationId,omitempty"`
	Code           string    `json:"code"`
	Label          string    `json:"label"`
	StartDate      string    `json:"start"`
	EndDate        string    `json:"end"`
	IsActive       bool      `json:"isActive"`
	CreatedAt      time.Time `json:"createdAt,omitempty"`
}

// EngineSettings configures hard filter tolerance and conflict thresholds per organization.
type EngineSettings struct {
	Preset         PresetID `json:"preset"`
	LevelTolerance int      `json:"levelTolerance"`
	OverloadAt     float64  `json:"overloadAt"`
	ReserveRatio   float64  `json:"reserveRatio"`
}

// DefaultEngineSettings returns safe default recommendation settings.
func DefaultEngineSettings() EngineSettings {
	return EngineSettings{
		Preset:         PresetSeimbang,
		LevelTolerance: 1,
		OverloadAt:     0.9,
		ReserveRatio:   0,
	}
}

// HRSettings stores organization-wide standard weekly hours and reserve capacity hours.
type HRSettings struct {
	WeeklyHours  float64 `json:"weeklyHours"`
	ReserveHours float64 `json:"reserveHours"`
}

// DefaultHRSettings returns safe defaults matching frontend /hr/pengaturan.
func DefaultHRSettings() HRSettings {
	return HRSettings{
		WeeklyHours:  40,
		ReserveHours: 0,
	}
}

// PerformanceCriterion defines a configurable manual review dimension.
type PerformanceCriterion struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Weight float64 `json:"weight"`
	Active bool    `json:"active"`
}

// PerformanceConfig controls automatic vs manager weight and criteria catalog.
type PerformanceConfig struct {
	AutomaticWeight float64                `json:"automaticWeight"`
	Criteria        []PerformanceCriterion `json:"criteria"`
}

// DefaultPerformanceConfig matches DEFAULT_PERFORMANCE_CONFIG in frontend/src/lib/hr/performance.ts.
func DefaultPerformanceConfig() PerformanceConfig {
	return PerformanceConfig{
		AutomaticWeight: 50,
		Criteria: []PerformanceCriterion{
			{ID: "collaboration", Name: "Kolaborasi", Weight: 50, Active: true},
			{ID: "responsibility", Name: "Tanggung jawab", Weight: 50, Active: true},
		},
	}
}

// PerformanceReviewCriterion records the weighted score snapshot at review time.
type PerformanceReviewCriterion struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Weight float64 `json:"weight"`
	Score  float64 `json:"score"`
}

// PerformanceReview stores a monthly manager review plus automatic score snapshot (never used for recommendations).
type PerformanceReview struct {
	ID              string                       `json:"id"`
	OrganizationID  string                       `json:"organizationId,omitempty"`
	MemberID        string                       `json:"memberId"`
	Period          string                       `json:"period"`
	Note            string                       `json:"note"`
	Author          string                       `json:"author"`
	AutomaticWeight float64                      `json:"automaticWeight"`
	AutomaticScore  *float64                     `json:"automaticScore"`
	OnTimeScore     *float64                     `json:"onTimeScore"`
	QualityScore    *float64                     `json:"qualityScore"`
	Criteria        []PerformanceReviewCriterion `json:"criteria"`
	CreatedAt       time.Time                    `json:"createdAt,omitempty"`
}

// HRRequest represents an inbox submission sent to HR by managers or team members.
type HRRequest struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organizationId,omitempty"`
	Sender         string `json:"sender"`
	SenderRole     string `json:"senderRole"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	CreatedAt      string `json:"createdAt"`
	Handled        bool   `json:"handled,omitempty"`
	Read           bool   `json:"read"`
}
