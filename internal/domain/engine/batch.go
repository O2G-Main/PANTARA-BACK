package engine

import (
	"fmt"
	"sort"

	"github.com/Orang2-BU/pantara-back/internal/domain/member"
	"github.com/Orang2-BU/pantara-back/internal/domain/task"
	"github.com/Orang2-BU/pantara-back/internal/domain/workspace"
)

// BatchPair pairs one open task with its suggested assignee or failure reason.
type BatchPair struct {
	Task     task.Task `json:"task"`
	MemberID *string   `json:"memberId"`
	Score    float64   `json:"score"`
	Reason   string    `json:"reason"`
}

var priorityRank = map[task.Priority]int{
	task.PriorityHigh:   0,
	task.PriorityNormal: 1,
	task.PriorityLow:    2,
}

// AllocateBatch computes an optimal multi-task assignment plan using bounded branch-and-bound
// initialized from priority-ordered greedy recommendations, updating live capacity per step.
func AllocateBatch(
	open []task.Task,
	members []member.Member,
	tasks []task.Task,
	settings workspace.EngineSettings,
	names map[string]string,
	today string,
) []BatchPair {
	ordered := make([]task.Task, len(open))
	copy(ordered, open)
	sort.SliceStable(ordered, func(i, j int) bool {
		pi, pj := priorityRank[ordered[i].Priority], priorityRank[ordered[j].Priority]
		if pi != pj {
			return pi < pj
		}
		if ordered[i].DueDate != ordered[j].DueDate {
			return ordered[i].DueDate < ordered[j].DueDate
		}
		return ordered[i].ID < ordered[j].ID
	})

	if len(ordered) == 0 {
		return nil
	}

	// Fast optimal search via greedy warm-start + branch-and-bound when batch size <= 16.
	// Falls back to greedy sequential pass if batch is very large to guarantee sub-second latency.
	greedyPairs := allocateGreedy(ordered, members, tasks, settings, names, today)
	if len(ordered) > 16 {
		return greedyPairs
	}

	bestAssign := make([]*BatchPair, len(ordered))
	bestCount := 0
	bestScore := -1.0
	for i := range greedyPairs {
		p := greedyPairs[i]
		bestAssign[i] = &p
		if p.MemberID != nil {
			bestCount++
			bestScore += p.Score
		}
	}

	working := make([]task.Task, len(tasks))
	copy(working, tasks)
	current := make([]*BatchPair, len(ordered))
	steps := 0
	const maxSteps = 8000

	var dfs func(idx, assigned int, totalScore float64)
	dfs = func(idx, assigned int, totalScore float64) {
		steps++
		if steps > maxSteps {
			return
		}
		rem := len(ordered) - idx
		if assigned+rem < bestCount {
			return
		}
		if assigned+rem == bestCount && totalScore+float64(rem) <= bestScore+1e-9 {
			return
		}
		if idx == len(ordered) {
			if assigned > bestCount || (assigned == bestCount && totalScore > bestScore+1e-9) {
				bestCount = assigned
				bestScore = totalScore
				for i := range current {
					cp := *current[i]
					bestAssign[i] = &cp
				}
			}
			return
		}

		t := ordered[idx]
		res := RankCandidates(t, members, working, settings, names, today)
		limit := len(res.Candidates)
		if limit > 3 {
			limit = 3
		}
		for cIdx := 0; cIdx < limit; cIdx++ {
			cand := res.Candidates[cIdx]
			mid := cand.Member.ID
			reason := ""
			if len(cand.Reasons) > 0 {
				reason = cand.Reasons[0]
			}
			current[idx] = &BatchPair{Task: t, MemberID: &mid, Score: cand.Score, Reason: reason}
			updated := appendTaskAssignment(working, t, mid, res.Effort)
			prev := working
			working = updated
			dfs(idx+1, assigned+1, totalScore+cand.Score)
			working = prev
		}

		// Explore leaving task unassigned only when needed to avoid blocking higher-count downstream matches
		if assigned+(rem-1) >= bestCount {
			current[idx] = &BatchPair{Task: t, MemberID: nil, Score: 0, Reason: explainUnassigned(res.Filtered)}
			dfs(idx+1, assigned, totalScore)
		}
	}

	dfs(0, 0, 0)

	out := make([]BatchPair, len(ordered))
	for i := range bestAssign {
		out[i] = *bestAssign[i]
	}
	return out
}

func allocateGreedy(
	ordered []task.Task,
	members []member.Member,
	tasks []task.Task,
	settings workspace.EngineSettings,
	names map[string]string,
	today string,
) []BatchPair {
	working := make([]task.Task, len(tasks))
	copy(working, tasks)
	pairs := make([]BatchPair, 0, len(ordered))

	for _, t := range ordered {
		res := RankCandidates(t, members, working, settings, names, today)
		if len(res.Candidates) == 0 {
			pairs = append(pairs, BatchPair{
				Task:     t,
				MemberID: nil,
				Score:    0,
				Reason:   explainUnassigned(res.Filtered),
			})
			continue
		}
		best := res.Candidates[0]
		mid := best.Member.ID
		reason := ""
		if len(best.Reasons) > 0 {
			reason = best.Reasons[0]
		}
		pairs = append(pairs, BatchPair{
			Task:     t,
			MemberID: &mid,
			Score:    best.Score,
			Reason:   reason,
		})
		working = appendTaskAssignment(working, t, mid, res.Effort)
	}
	return pairs
}

func appendTaskAssignment(working []task.Task, t task.Task, memberID string, effort float64) []task.Task {
	next := make([]task.Task, len(working))
	copy(next, working)
	replaced := false
	for i := range next {
		if next[i].ID == t.ID {
			next[i].Status = task.StatusWaiting
			next[i].Assignments = []task.Assignment{{MemberID: memberID, Hours: effort}}
			next[i].RemainingHours = effort
			replaced = true
			break
		}
	}
	if !replaced {
		tCopy := t
		tCopy.Status = task.StatusWaiting
		tCopy.Assignments = []task.Assignment{{MemberID: memberID, Hours: effort}}
		tCopy.RemainingHours = effort
		next = append(next, tCopy)
	}
	return next
}

func explainUnassigned(filtered []FilteredCandidate) string {
	if len(filtered) == 0 {
		return "Tidak ada kandidat tersedia"
	}
	sorted := make([]FilteredCandidate, len(filtered))
	copy(sorted, filtered)
	sort.SliceStable(sorted, func(i, j int) bool {
		return len(sorted[i].Failures) < len(sorted[j].Failures)
	})
	firstFail := ""
	if len(sorted[0].Failures) > 0 {
		firstFail = sorted[0].Failures[0]
	}
	return fmt.Sprintf("Tidak ada yang lolos (%s: %s)", sorted[0].Member.Name, firstFail)
}
