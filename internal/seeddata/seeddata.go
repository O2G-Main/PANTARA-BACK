package seeddata

import (
	_ "embed"
	"encoding/json"

	"github.com/Orang2-BU/pantara-back/internal/domain/decision"
	"github.com/Orang2-BU/pantara-back/internal/domain/member"
	"github.com/Orang2-BU/pantara-back/internal/domain/project"
	"github.com/Orang2-BU/pantara-back/internal/domain/task"
)

//go:embed arunika_seed.json
var arunikaRaw []byte

// Dataset mirrors the canonical Arunika Digital dataset exported from frontend/src/lib/demo/seed.ts.
type Dataset struct {
	Period struct {
		Start string `json:"start"`
		End   string `json:"end"`
		Label string `json:"label"`
	} `json:"period"`
	Now       string `json:"now"`
	Workspace struct {
		Name         string  `json:"name"`
		HoursPerWeek float64 `json:"hoursPerWeek"`
	} `json:"workspace"`
	BaselineMinutes float64             `json:"baselineMinutes"`
	Competencies    []member.Competency `json:"competencies"`
	Members         []member.Member     `json:"members"`
	Projects        []project.Project   `json:"projects"`
	Tasks           []task.Task         `json:"tasks"`
	Decisions       []decision.Decision `json:"decisions"`
}

// LoadArunika decodes a fresh copy of the canonical Arunika Digital seed dataset.
func LoadArunika() (Dataset, error) {
	var ds Dataset
	if err := json.Unmarshal(arunikaRaw, &ds); err != nil {
		return Dataset{}, err
	}
	for i := range ds.Members {
		ds.Members[i].IsActive = true
	}
	return ds, nil
}
