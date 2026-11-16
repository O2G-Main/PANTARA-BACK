package engine

import (
	"fmt"
	"math"

	"github.com/Orang2-BU/pantara-back/internal/domain/member"
	"github.com/Orang2-BU/pantara-back/internal/domain/task"
)

const DefaultOverloadThreshold = 0.85

// UtilTone maps utilization ratio to UI severity tone ("ok" | "warn" | "crit").
func UtilTone(util float64, warnAt ...float64) string {
	threshold := DefaultOverloadThreshold
	if len(warnAt) > 0 && warnAt[0] > 0 && warnAt[0] < 1 {
		threshold = warnAt[0]
	}
	if util >= 1 {
		return "crit"
	}
	if util >= threshold {
		return "warn"
	}
	return "ok"
}

// Conflict describes a capacity, deadline, or dependency risk detected before assignment.
type Conflict struct {
	Tone     string `json:"tone"` // "warn" | "crit"
	Text     string `json:"text"`
	MemberID string `json:"memberId,omitempty"`
}

// SimulationRow compares one member's load before and after adding a scenario's hours.
type SimulationRow struct {
	Member    member.Member `json:"member"`
	Before    float64       `json:"before"`
	After     float64       `json:"after"`
	Added     float64       `json:"added"`
	Available float64       `json:"available"`
}

// SimulationResult captures team-wide what-if impact prior to committing an assignment.
type SimulationResult struct {
	Rows       []SimulationRow `json:"rows"`
	Conflicts  []Conflict      `json:"conflicts"`
	PeakBefore float64         `json:"peakBefore"`
	PeakAfter  float64         `json:"peakAfter"`
	TeamBefore float64         `json:"teamBefore"`
	TeamAfter  float64         `json:"teamAfter"`
}

// Simulate computes hypothetical utilization impact and pre-assignment warnings without mutating tasks.
func Simulate(
	t task.Task,
	scenario []task.Assignment,
	members []member.Member,
	tasks []task.Task,
	warnThreshold ...float64,
) SimulationResult {
	overloadAt := DefaultOverloadThreshold
	if len(warnThreshold) > 0 && warnThreshold[0] > 0 && warnThreshold[0] < 1 {
		overloadAt = warnThreshold[0]
	}

	addedByMember := make(map[string]float64, len(scenario))
	for _, s := range scenario {
		addedByMember[s.MemberID] += s.Hours
	}

	rows := make([]SimulationRow, 0, len(members))
	for _, m := range members {
		if m.AvailableHours == nil {
			continue
		}
		before := AllocatedHours(m.ID, tasks)
		added := addedByMember[m.ID]
		rows = append(rows, SimulationRow{
			Member:    m,
			Before:    before,
			After:     before + added,
			Added:     added,
			Available: *m.AvailableHours,
		})
	}

	conflicts := make([]Conflict, 0)
	for _, s := range scenario {
		var row *SimulationRow
		for i := range rows {
			if rows[i].Member.ID == s.MemberID {
				row = &rows[i]
				break
			}
		}
		if row == nil || row.Available <= 0 {
			continue
		}
		util := row.After / row.Available
		if util >= 1 {
			conflicts = append(conflicts, Conflict{
				Tone:     "crit",
				MemberID: row.Member.ID,
				Text:     fmt.Sprintf("%s menjadi %d%% (%s dari %s jam), melebihi kapasitas periode", row.Member.Name, RoundIntPct(util), fmtNumber(row.After, 1), fmtNumber(row.Available, 0)),
			})
		} else if util >= overloadAt {
			conflicts = append(conflicts, Conflict{
				Tone:     "warn",
				MemberID: row.Member.ID,
				Text:     fmt.Sprintf("%s menjadi %d%%, mendekati penuh", row.Member.Name, RoundIntPct(util)),
			})
		}

		near := NearbyDeadlines(row.Member.ID, t.DueDate, tasks, t.ID)
		if len(near) > 0 {
			conflicts = append(conflicts, Conflict{
				Tone:     "warn",
				MemberID: row.Member.ID,
				Text:     fmt.Sprintf("%s punya %d tugas lain dengan tenggat berdekatan", row.Member.Name, len(near)),
			})
		}
	}

	if t.DNA != nil {
		taskByID := make(map[string]task.Task, len(tasks))
		for _, item := range tasks {
			taskByID[item.ID] = item
		}
		for _, dep := range t.DNA.Dependencies {
			other, ok := taskByID[dep.TaskID]
			if !ok || other.Status != task.StatusDone {
				title := dep.TaskID
				status := "belum selesai"
				if ok {
					title = other.Title
					status = string(other.Status)
				}
				conflicts = append(conflicts, Conflict{
					Tone: "warn",
					Text: fmt.Sprintf("Bergantung pada \"%s\" yang masih %s", title, status),
				})
			}
		}
	}

	var peakBefore, peakAfter, sumBefore, sumAfter, totalCap float64
	for _, r := range rows {
		if r.Available > 0 {
			ub := r.Before / r.Available
			ua := r.After / r.Available
			if ub > peakBefore {
				peakBefore = ub
			}
			if ua > peakAfter {
				peakAfter = ua
			}
		}
		sumBefore += r.Before
		sumAfter += r.After
		totalCap += r.Available
	}

	var teamBefore, teamAfter float64
	if totalCap > 0 {
		teamBefore = sumBefore / totalCap
		teamAfter = sumAfter / totalCap
	}

	return SimulationResult{
		Rows:       rows,
		Conflicts:  conflicts,
		PeakBefore: peakBefore,
		PeakAfter:  peakAfter,
		TeamBefore: teamBefore,
		TeamAfter:  teamAfter,
	}
}

// SplitHours divides effort into two integer/decimal parts with primaryShare going first.
func SplitHours(effort, primaryShare float64) [2]float64 {
	if primaryShare <= 0 {
		primaryShare = 0.5
	}
	first := math.Floor(effort*primaryShare + 0.5)
	return [2]float64{first, effort - first}
}
