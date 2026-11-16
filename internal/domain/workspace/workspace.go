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
}

// DefaultEngineSettings returns safe default recommendation settings.
func DefaultEngineSettings() EngineSettings {
	return EngineSettings{
		Preset:         PresetSeimbang,
		LevelTolerance: 1,
		OverloadAt:     0.9,
	}
}
