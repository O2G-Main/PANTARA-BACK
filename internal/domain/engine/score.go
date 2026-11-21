package engine

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Orang2-BU/pantara-back/internal/domain/member"
	"github.com/Orang2-BU/pantara-back/internal/domain/task"
	"github.com/Orang2-BU/pantara-back/internal/domain/workspace"
)

const (
	TieGap      = 0.03
	OneDayMillis = 86_400_000
)

// LevelLabels renders human competency levels in Indonesian.
var LevelLabels = [4]string{"Belum", "Dasar", "Mandiri", "Mahir"}

// Weights represents the 5 factor multipliers in S = w1*K + w2*C + w3*A + w4*R - w5*F.
type Weights struct {
	K float64 `json:"k"`
	C float64 `json:"c"`
	A float64 `json:"a"`
	R float64 `json:"r"`
	F float64 `json:"f"`
}

// PresetDef describes a named weight profile and optional knowledge-spread bonus.
type PresetDef struct {
	Label       string  `json:"label"`
	When        string  `json:"when"`
	Weights     Weights `json:"weights"`
	SpreadBonus float64 `json:"spreadBonus"`
}

// Presets contains the four canonical scoring profiles defined in the PRD.
var Presets = map[workspace.PresetID]PresetDef{
	workspace.PresetSeimbang: {
		Label:       "Seimbang",
		When:        "Standar harian, kompromi keahlian, beban, dan tenggat.",
		Weights:     Weights{K: 0.35, C: 0.25, A: 0.15, R: 0.15, F: 0.10},
		SpreadBonus: 0,
	},
	workspace.PresetTenggat: {
		Label:       "Kejar tenggat",
		When:        "Dipakai saat tenggat ketat, ketersediaan waktu diberi bobot lebih tinggi.",
		Weights:     Weights{K: 0.30, C: 0.15, A: 0.30, R: 0.15, F: 0.10},
		SpreadBonus: 0,
	},
	workspace.PresetRatakan: {
		Label:       "Ratakan beban",
		When:        "Dipakai saat tim padat, menghindari penumpukan di satu orang.",
		Weights:     Weights{K: 0.25, C: 0.40, A: 0.15, R: 0.05, F: 0.15},
		SpreadBonus: 0,
	},
	workspace.PresetSebar: {
		Label:       "Sebar pengetahuan",
		When:        "Memberi kesempatan orang tanpa riwayat sejenis supaya keahlian tidak terpusat.",
		Weights:     Weights{K: 0.30, C: 0.25, A: 0.15, R: 0.00, F: 0.10},
		SpreadBonus: 0.20,
	},
}

// SourceConfidence attenuates self-claimed skills vs manager-verified and historical skills.
var SourceConfidence = map[member.CompetencySource]float64{
	member.SourceVerified: 1.0,
	member.SourceHistory:  0.9,
	member.SourceClaim:    0.7,
}

// EffectiveWeights boosts competency weight for high-complexity tasks on the Seimbang preset.
func EffectiveWeights(preset workspace.PresetID, complexity member.Level) Weights {
	def, ok := Presets[preset]
	if !ok {
		def = Presets[workspace.PresetSeimbang]
	}
	w := def.Weights
	if preset == workspace.PresetSeimbang && complexity >= member.LevelIndep {
		w.K = Round2(w.K + 0.10)
		w.C = Round2(w.C - 0.05)
		w.R = Round2(w.R - 0.05)
	}
	return w
}

// AllocatedHours sums assigned hours for m across non-draft, non-open tasks.
func AllocatedHours(memberID string, tasks []task.Task) float64 {
	var total float64
	for _, t := range tasks {
		if t.Status == task.StatusDraft || t.Status == task.StatusOpen {
			continue
		}
		for _, a := range t.Assignments {
			if a.MemberID == memberID {
				total += a.Hours
			}
		}
	}
	return total
}

// MemberLevel looks up a member's skill level for competencyID (0 if absent).
func MemberLevel(m member.Member, competencyID string) member.Level {
	for _, c := range m.Competencies {
		if c.ID == competencyID {
			return c.Level
		}
	}
	return member.LevelNone
}

func findMemberComp(m member.Member, competencyID string) (member.MemberCompetency, bool) {
	for _, c := range m.Competencies {
		if c.ID == competencyID {
			return c, true
		}
	}
	return member.MemberCompetency{}, false
}

// NearbyDeadlines returns active tasks assigned to memberID with due dates within +-1 day of dueDate.
func NearbyDeadlines(memberID, dueDate string, tasks []task.Task, excludeTaskID string) []task.Task {
	ref, err := time.Parse("2006-01-02", dueDate)
	if err != nil {
		return nil
	}
	var out []task.Task
	for _, t := range tasks {
		if t.ID == excludeTaskID || !t.Status.IsActive() {
			continue
		}
		assigned := false
		for _, a := range t.Assignments {
			if a.MemberID == memberID {
				assigned = true
				break
			}
		}
		if !assigned {
			continue
		}
		td, err := time.Parse("2006-01-02", t.DueDate)
		if err != nil {
			continue
		}
		diff := math.Abs(float64(td.Sub(ref) / time.Millisecond))
		if diff <= OneDayMillis {
			out = append(out, t)
		}
	}
	return out
}

// ClashingDeadline finds the earliest active due date for memberID having at least one nearby deadline.
func ClashingDeadline(memberID string, tasks []task.Task) string {
	var active []task.Task
	for _, t := range tasks {
		if !t.Status.IsActive() {
			continue
		}
		for _, a := range t.Assignments {
			if a.MemberID == memberID {
				active = append(active, t)
				break
			}
		}
	}
	sort.Slice(active, func(i, j int) bool {
		return active[i].DueDate < active[j].DueDate
	})
	for _, t := range active {
		if len(NearbyDeadlines(memberID, t.DueDate, tasks, t.ID)) > 0 {
			return t.DueDate
		}
	}
	return ""
}

// Candidate holds deterministic scoring output and human-readable explanations.
type Candidate struct {
	Member        member.Member `json:"member"`
	Score         float64       `json:"score"`
	Factors       Weights       `json:"factors"`
	Allocated     float64       `json:"allocated"`
	Available     float64       `json:"available"`
	Free          float64       `json:"free"`
	UtilBefore    float64       `json:"utilBefore"`
	UtilAfter     float64       `json:"utilAfter"`
	UtilAfterHigh float64       `json:"utilAfterHigh"`
	History       int           `json:"history"`
	TiedWithPrev  bool          `json:"tiedWithPrev"`
	Reasons       []string      `json:"reasons"`
	Cautions      []string      `json:"cautions"`
}

// FilteredCandidate explains why an ineligible member failed mandatory gates.
type FilteredCandidate struct {
	Member   member.Member `json:"member"`
	Failures []string      `json:"failures"`
}

// RankResult packages eligible ranked candidates, filtered members, and effort baseline.
type RankResult struct {
	Candidates []Candidate         `json:"candidates"`
	Filtered   []FilteredCandidate `json:"filtered"`
	Effort     float64             `json:"effort"`
}

func containsStr(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}

// RankCandidates applies hard eligibility filters then scores and explains remaining candidates.
func RankCandidates(
	t task.Task,
	members []member.Member,
	tasks []task.Task,
	settings workspace.EngineSettings,
	names map[string]string,
	today string,
) RankResult {
	var dna *task.TaskDNA
	if t.DNA != nil {
		dna = t.DNA
	}
	var reqs []task.Requirement
	var complexity member.Level = member.LevelBasic
	if dna != nil {
		reqs = dna.Requirements
		complexity = dna.Complexity
	}
	reqIDs := make([]string, len(reqs))
	for i, r := range reqs {
		reqIDs[i] = r.CompetencyID
	}

	effort := t.EffortOrRemaining()
	overrun := OverrunFactor(tasks)
	presetDef, ok := Presets[settings.Preset]
	if !ok {
		presetDef = Presets[workspace.PresetSeimbang]
	}
	w := EffectiveWeights(settings.Preset, complexity)
	// Kapasitas cadangan hanya terbuka untuk tugas prioritas tinggi.
	openRatio := 1.0
	if t.Priority != task.PriorityHigh {
		rr := settings.ReserveRatio
		if rr < 0 {
			rr = 0
		} else if rr > 0.3 {
			rr = 0.3
		}
		openRatio = 1.0 - rr
	}

	candidates := make([]Candidate, 0, len(members))
	filtered := make([]FilteredCandidate, 0)

	for _, m := range members {
		if m.ID == "" {
			continue
		}
		if m.AvailableHours == nil {
			filtered = append(filtered, FilteredCandidate{
				Member:   m,
				Failures: []string{"Ketersediaan periode belum diisi"},
			})
			continue
		}
		available := *m.AvailableHours * openRatio
		var failures []string

		work := Workdays(today, t.DueDate)
		leaveSet := make(map[string]struct{}, len(m.LeaveDates))
		for _, d := range m.LeaveDates {
			leaveSet[d] = struct{}{}
		}
		leaveDays := 0
		for _, d := range work {
			if _, off := leaveSet[d]; off {
				leaveDays++
			}
		}

		free := HoursUntil(m, t.DueDate, today)*openRatio - CommittedUntil(m.ID, t.DueDate, tasks, t.ID)
		if len(work) > 0 && leaveDays == len(work) {
			failures = append(failures, "Cuti di semua hari kerja sampai tenggat")
		} else if free < effort*0.5 {
			failures = append(failures, "Sisa jam sampai tenggat terlalu sedikit")
		}

		for _, r := range reqs {
			minLvl := int(r.Level) - settings.LevelTolerance
			if minLvl < 0 {
				minLvl = 0
			}
			have := int(MemberLevel(m, r.CompetencyID))
			if have < minLvl {
				label := names[r.CompetencyID]
				if label == "" {
					label = r.CompetencyID
				}
				if have == 0 {
					failures = append(failures, fmt.Sprintf("%s belum dimiliki, butuh minimal %s", label, LevelLabels[minLvl]))
				} else {
					failures = append(failures, fmt.Sprintf("%s baru %s, butuh minimal %s", label, LevelLabels[have], LevelLabels[minLvl]))
				}
			}
		}

		allocated := AllocatedHours(m.ID, tasks)
		if allocated+effort > m.WeeklyLimit {
			failures = append(failures, fmt.Sprintf("Melewati batas %s jam per minggu", fmtRoundedInt(m.WeeklyLimit)))
		}

		if len(failures) > 0 {
			filtered = append(filtered, FilteredCandidate{Member: m, Failures: failures})
			continue
		}

		var sumPRatio, sumP float64
		for _, r := range reqs {
			sumP += r.P
			comp, found := findMemberComp(m, r.CompetencyID)
			if !found {
				continue
			}
			conf := SourceConfidence[comp.Source]
			if conf == 0 {
				conf = 0.7
			}
			lvlRatio := float64(comp.Level) / math.Max(float64(r.Level), 1)
			if lvlRatio > 1 {
				lvlRatio = 1
			}
			sumPRatio += r.P * lvlRatio * conf
		}
		k := 1.0
		if sumP > 0 {
			k = sumPRatio / sumP
		}

		var utilBefore, utilAfter, utilAfterHigh float64
		if available > 0 {
			utilBefore = allocated / available
			utilAfter = (allocated + effort) / available
			utilAfterHigh = (allocated + effort*overrun.Factor) / available
		} else {
			utilAfter = 1
			utilAfterHigh = 1
		}
		c := LoadScore(utilAfter)

		aDenom := effort
		if aDenom < 0.5 {
			aDenom = 0.5
		}
		a := Clamp01(free / aDenom)

		history := 0
		for _, done := range tasks {
			if done.Status != task.StatusDone {
				continue
			}
			assigned := false
			for _, asgn := range done.Assignments {
				if asgn.MemberID == m.ID {
					assigned = true
					break
				}
			}
			if !assigned {
				continue
			}
			overlap := false
			for _, cid := range done.CompetencyIDs {
				if containsStr(reqIDs, cid) {
					overlap = true
					break
				}
			}
			if overlap {
				history++
			}
		}

		pref := 0.5
		if len(reqs) > 0 {
			topReq := reqs[0]
			for _, r := range reqs[1:] {
				if r.P > topReq.P {
					topReq = r
				}
			}
			if containsStr(m.Prefers, topReq.CompetencyID) {
				pref = 1.0
			} else if containsStr(m.Avoids, topReq.CompetencyID) {
				pref = 0.0
			}
		}
		r := Clamp01(0.7*Clamp01(float64(history)/3.0) + 0.3*pref)

		nearby := len(NearbyDeadlines(m.ID, t.DueDate, tasks, t.ID))
		f := Clamp01(0.5 * float64(nearby))

		bonus := 0.0
		if history == 0 {
			bonus = presetDef.SpreadBonus
		}

		scoreVal := Clamp01(w.K*k + w.C*c + w.A*a + w.R*r - w.F*f + bonus)
		reasons, cautions := explain(m, reqs, Weights{K: k, C: c, A: a, R: r, F: f}, utilAfter, utilAfterHigh, free, nearby, history, leaveDays, bonus, names)

		candidates = append(candidates, Candidate{
			Member:        m,
			Score:         scoreVal,
			Factors:       Weights{K: k, C: c, A: a, R: r, F: f},
			Allocated:     allocated,
			Available:     available,
			Free:          free,
			UtilBefore:    utilBefore,
			UtilAfter:     utilAfter,
			UtilAfterHigh: utilAfterHigh,
			History:       history,
			Reasons:       reasons,
			Cautions:      cautions,
		})
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		if candidates[i].UtilAfter != candidates[j].UtilAfter {
			return candidates[i].UtilAfter < candidates[j].UtilAfter
		}
		return candidates[i].Member.ID < candidates[j].Member.ID
	})

	for i := range candidates {
		if i > 0 && candidates[i-1].Score-candidates[i].Score < TieGap {
			candidates[i].TiedWithPrev = true
		}
	}

	return RankResult{
		Candidates: candidates,
		Filtered:   filtered,
		Effort:     effort,
	}
}

func explain(
	m member.Member,
	reqs []task.Requirement,
	f Weights,
	utilAfter, utilAfterHigh, free float64,
	nearby, history, leaveDays int,
	bonus float64,
	names map[string]string,
) (reasons, cautions []string) {
	reasons = []string{}
	cautions = []string{}

	exact := true
	for _, r := range reqs {
		if MemberLevel(m, r.CompetencyID) < r.Level {
			exact = false
			break
		}
	}
	if exact {
		reasons = append(reasons, "Memenuhi semua level kompetensi yang dibutuhkan")
	} else {
		reasons = append(reasons, "Level kompetensi masih dalam toleransi, perlu pendampingan ringan")
	}

	var claimed []string
	for _, r := range reqs {
		if comp, ok := findMemberComp(m, r.CompetencyID); ok && comp.Source == member.SourceClaim {
			label := names[r.CompetencyID]
			if label == "" {
				label = r.CompetencyID
			}
			claimed = append(claimed, label)
		}
	}
	if len(claimed) > 0 {
		cautions = append(cautions, fmt.Sprintf("Kompetensi %s masih klaim sendiri, belum diverifikasi manajer", strings.Join(claimed, ", ")))
	}

	if f.A >= 1 {
		reasons = append(reasons, fmt.Sprintf("Tersedia %s jam kosong sebelum tenggat", fmtNumber(free, 1)))
	} else {
		cautions = append(cautions, fmt.Sprintf("Jam tersisa %s jam sampai tenggat, agak mepet", fmtNumber(free, 1)))
	}

	if leaveDays > 0 {
		cautions = append(cautions, fmt.Sprintf("Ada %d hari cuti sebelum tenggat (sudah dihitung di ketersediaan)", leaveDays))
	}
	if history > 0 {
		reasons = append(reasons, fmt.Sprintf("Pernah menyelesaikan %d tugas dengan kompetensi serupa", history))
	}
	if bonus > 0 {
		reasons = append(reasons, "Memberi kesempatan menangani kompetensi baru")
	}

	if utilAfter >= 1 {
		cautions = append(cautions, fmt.Sprintf("Beban menjadi %d%%, melebihi kapasitas periode", RoundIntPct(utilAfter)))
	} else if utilAfter >= 0.85 {
		cautions = append(cautions, fmt.Sprintf("Beban setelah penugasan %d%%, mendekati penuh", RoundIntPct(utilAfter)))
	} else {
		reasons = append(reasons, fmt.Sprintf("Beban setelah penugasan %d%%, masih aman", RoundIntPct(utilAfter)))
	}

	if utilAfter < 1 && utilAfterHigh >= 1 {
		cautions = append(cautions, fmt.Sprintf("Bila tugas molor seperti biasanya, beban bisa mencapai %d%%", RoundIntPct(utilAfterHigh)))
	}

	if nearby > 0 {
		cautions = append(cautions, fmt.Sprintf("Ada %d tugas aktif dengan tenggat berdekatan", nearby))
	}
	return reasons, cautions
}
