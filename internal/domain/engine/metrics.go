package engine

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/Orang2-BU/pantara-back/internal/domain/decision"
	"github.com/Orang2-BU/pantara-back/internal/domain/member"
	"github.com/Orang2-BU/pantara-back/internal/domain/project"
	"github.com/Orang2-BU/pantara-back/internal/domain/task"
)

const SevenDaysMillis = 7 * OneDayMillis

// DecisionStats summarizes decision efficiency, acceptance rate, and conflict exposure.
type DecisionStats struct {
	N         int     `json:"n"`
	Assigned  int     `json:"assigned"`
	Accepted  int     `json:"accepted"`
	Override  int     `json:"override"`
	Rejected  int     `json:"rejected"`
	AvgMs     float64 `json:"avgMs"`
	Conflicts int     `json:"conflicts"`
}

// StabilityStats summarizes the 7-day reassignment North Star metric.
type StabilityStats struct {
	Stable  int     `json:"stable"`
	Moved   int     `json:"moved"`
	Pending int     `json:"pending"`
	Rate    float64 `json:"rate"`
}

// AccuracyStats captures average actual/planned effort calibration.
type AccuracyStats struct {
	N     int     `json:"n"`
	Ratio float64 `json:"ratio"`
}

// AtRiskSkill flags competencies with <= 1 holder at Mandiri level or above.
type AtRiskSkill struct {
	Competency member.Competency `json:"competency"`
	Holders    []member.Member   `json:"holders"`
}

// ComputeDecisionStats aggregates counts and mean decision duration over assigned decisions.
func ComputeDecisionStats(decisions []decision.Decision) DecisionStats {
	s := DecisionStats{N: len(decisions)}
	var totalMs int64
	for _, d := range decisions {
		s.Conflicts += d.ConflictsSeen
		if d.Rejected {
			s.Rejected++
			continue
		}
		s.Assigned++
		totalMs += d.DecisionMs
		if d.Rank != nil {
			s.Accepted++
		} else {
			s.Override++
		}
	}
	if s.Assigned > 0 {
		s.AvgMs = float64(totalMs) / float64(s.Assigned)
	}
	return s
}

func sameMembers(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sa := append([]string(nil), a...)
	sb := append([]string(nil), b...)
	sort.Strings(sa)
	sort.Strings(sb)
	return strings.Join(sa, ",") == strings.Join(sb, ",")
}

// ComputeStabilityRate calculates 7-day non-reassignment stability relative to nowISO.
func ComputeStabilityRate(decisions []decision.Decision, nowISO string) StabilityStats {
	nowT, err := time.Parse(time.RFC3339, nowISO)
	if err != nil {
		nowT = time.Now().UTC()
	}
	var assigned []decision.Decision
	for _, d := range decisions {
		if !d.Rejected {
			assigned = append(assigned, d)
		}
	}

	var stable, moved, pending int
	for _, d := range assigned {
		atT, err := time.Parse(time.RFC3339, d.DecidedAt)
		if err != nil {
			continue
		}
		reassigned := false
		for _, o := range assigned {
			if o.ID == d.ID || o.TaskID != d.TaskID {
				continue
			}
			oT, err := time.Parse(time.RFC3339, o.DecidedAt)
			if err != nil {
				continue
			}
			dtMs := oT.Sub(atT).Milliseconds()
			if dtMs > 0 && dtMs <= SevenDaysMillis && !sameMembers(d.MemberIDs, o.MemberIDs) {
				reassigned = true
				break
			}
		}
		if reassigned {
			moved++
		} else if nowT.Sub(atT).Milliseconds() < SevenDaysMillis {
			pending++
		} else {
			stable++
		}
	}

	rate := 0.0
	if stable+moved > 0 {
		rate = float64(stable) / float64(stable+moved)
	}
	return StabilityStats{Stable: stable, Moved: moved, Pending: pending, Rate: rate}
}

// ComputeEstimateAccuracy calculates mean actualHours / plannedHours on completed tasks.
func ComputeEstimateAccuracy(tasks []task.Task) AccuracyStats {
	var ratios []float64
	for _, t := range tasks {
		if t.Status != task.StatusDone || t.ActualHours == nil {
			continue
		}
		p := PlannedHours(t)
		if p <= 0 {
			continue
		}
		ratios = append(ratios, *t.ActualHours/p)
	}
	if len(ratios) == 0 {
		return AccuracyStats{}
	}
	var sum float64
	for _, r := range ratios {
		sum += r
	}
	return AccuracyStats{N: len(ratios), Ratio: Round2(sum / float64(len(ratios)))}
}

// AtRiskCompetencies identifies skills held by at most 1 member at level >= Mandiri (2).
func AtRiskCompetencies(competencies []member.Competency, members []member.Member) []AtRiskSkill {
	var out []AtRiskSkill
	for _, c := range competencies {
		var holders []member.Member
		for _, m := range members {
			if MemberLevel(m, c.ID) >= member.LevelIndep {
				holders = append(holders, m)
			}
		}
		if len(holders) <= 1 {
			out = append(out, AtRiskSkill{Competency: c, Holders: holders})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return len(out[i].Holders) < len(out[j].Holders)
	})
	return out
}

// Graph types and deterministic physics layout matching frontend relation graph.
type NodeKind string
type EdgeKind string

const (
	NodeMember     NodeKind = "karyawan"
	NodeCompetency NodeKind = "kompetensi"
	NodeTask       NodeKind = "tugas"
	NodeProject    NodeKind = "proyek"

	EdgeHas      EdgeKind = "menguasai"
	EdgeRequires EdgeKind = "membutuhkan"
	EdgeAssigned EdgeKind = "dikerjakan"
	EdgeBelongs  EdgeKind = "milik"
)

type GraphNode struct {
	ID     string  `json:"id"`
	Kind   NodeKind `json:"kind"`
	Ref    string  `json:"ref"`
	Label  string  `json:"label"`
	Sub    string  `json:"sub,omitempty"`
	Photo  string  `json:"photo,omitempty"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	VX     float64 `json:"vx"`
	VY     float64 `json:"vy"`
	R      float64 `json:"r"`
	Pinned bool    `json:"pinned,omitempty"`
}

type GraphEdge struct {
	Source string   `json:"source"`
	Target string   `json:"target"`
	Kind   EdgeKind `json:"kind"`
	Weight float64  `json:"weight"`
}

type Graph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// BuildGraph assembles nodes and edges from members, competencies, open tasks, and projects.
func BuildGraph(members []member.Member, competencies []member.Competency, tasks []task.Task, projects []project.Project, w, h float64) Graph {
	if w <= 0 {
		w = 900
	}
	if h <= 0 {
		h = 560
	}
	openTasks := make([]task.Task, 0)
	for _, t := range tasks {
		if t.Status != task.StatusDone {
			openTasks = append(openTasks, t)
		}
	}

	nodes := make([]GraphNode, 0, len(projects)+len(members)+len(competencies)+len(openTasks))
	edges := make([]GraphEdge, 0)

	place := func(itemsCount, idx int, kind NodeKind, ref, label, sub, photo string, ringFactor, radius float64) {
		n := math.Max(float64(itemsCount), 1)
		ang := (float64(idx)/n)*math.Pi*2 - math.Pi/2
		rx := w * ringFactor
		ry := h * ringFactor
		nodes = append(nodes, GraphNode{
			ID:    fmt.Sprintf("%s:%s", kind, ref),
			Kind:  kind,
			Ref:   ref,
			Label: label,
			Sub:   sub,
			Photo: photo,
			X:     Round2(w/2 + math.Cos(ang)*rx),
			Y:     Round2(h/2 + math.Sin(ang)*ry),
			R:     radius,
		})
	}

	for i, p := range projects {
		place(len(projects), i, NodeProject, p.ID, p.Name, p.Client, "", 0.12, 15)
	}
	for i, m := range members {
		place(len(members), i, NodeMember, m.ID, m.Name, m.Role, m.Photo, 0.28, 16)
	}
	for i, c := range competencies {
		place(len(competencies), i, NodeCompetency, c.ID, c.Name, c.Category, "", 0.44, 9)
	}
	for i, t := range openTasks {
		place(len(openTasks), i, NodeTask, t.ID, t.Title, string(t.Status), "", 0.30, 7)
	}

	for _, m := range members {
		for _, c := range m.Competencies {
			edges = append(edges, GraphEdge{
				Source: "karyawan:" + m.ID,
				Target: "kompetensi:" + c.ID,
				Kind:   EdgeHas,
				Weight: float64(c.Level),
			})
		}
	}
	for _, t := range openTasks {
		edges = append(edges, GraphEdge{
			Source: "tugas:" + t.ID,
			Target: "proyek:" + t.ProjectID,
			Kind:   EdgeBelongs,
			Weight: 1,
		})
		for _, a := range t.Assignments {
			edges = append(edges, GraphEdge{
				Source: "tugas:" + t.ID,
				Target: "karyawan:" + a.MemberID,
				Kind:   EdgeAssigned,
				Weight: 1,
			})
		}
		if t.DNA != nil {
			for _, r := range t.DNA.Requirements {
				edges = append(edges, GraphEdge{
					Source: "tugas:" + t.ID,
					Target: "kompetensi:" + r.CompetencyID,
					Kind:   EdgeRequires,
					Weight: float64(r.Level),
				})
			}
		}
	}

	return Graph{Nodes: nodes, Edges: edges}
}
