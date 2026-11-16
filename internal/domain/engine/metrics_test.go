package engine_test

import (
	"testing"

	"github.com/Orang2-BU/pantara-back/internal/domain/decision"
	"github.com/Orang2-BU/pantara-back/internal/domain/engine"
	"github.com/Orang2-BU/pantara-back/internal/domain/member"
	"github.com/Orang2-BU/pantara-back/internal/domain/task"
	"github.com/Orang2-BU/pantara-back/internal/domain/workspace"
	"github.com/Orang2-BU/pantara-back/internal/seeddata"
)

func intPtr(v int) *int { return &v }

func TestMetricsAndStability(t *testing.T) {
	decisions := []decision.Decision{
		{ID: "KPT-1", TaskID: "TGS-1", MemberIDs: []string{"bima"}, Rank: intPtr(1), Preset: workspace.PresetSeimbang, DecidedAt: "2026-09-29T09:00:00+07:00", DecisionMs: 6 * 60_000, ConflictsSeen: 1},
		{ID: "KPT-2", TaskID: "TGS-1", MemberIDs: []string{"nadia"}, Rank: nil, ReasonCode: "klien", Preset: workspace.PresetSeimbang, DecidedAt: "2026-09-29T09:00:00+07:00", DecisionMs: 10 * 60_000, ConflictsSeen: 1},
		{ID: "KPT-3", TaskID: "TGS-1", MemberIDs: []string{}, Rank: nil, Rejected: true, ReasonCode: "tidak-sesuai", Preset: workspace.PresetSeimbang, DecidedAt: "2026-09-29T09:00:00+07:00", DecisionMs: 4 * 60_000, ConflictsSeen: 1},
	}

	stats := engine.ComputeDecisionStats(decisions)
	if stats.N != 3 || stats.Accepted != 1 || stats.Override != 1 || stats.Rejected != 1 || stats.Conflicts != 3 || stats.AvgMs != 8*60_000 {
		t.Fatalf("unexpected decision stats: %+v", stats)
	}

	stab := engine.ComputeStabilityRate([]decision.Decision{
		{ID: "1", TaskID: "A", MemberIDs: []string{"x"}, Rank: intPtr(1), DecidedAt: "2026-09-01T09:00:00+07:00"},
		{ID: "2", TaskID: "A", MemberIDs: []string{"y"}, Rank: intPtr(1), DecidedAt: "2026-09-04T09:00:00+07:00"},
		{ID: "3", TaskID: "B", MemberIDs: []string{"x"}, Rank: intPtr(1), DecidedAt: "2026-09-02T09:00:00+07:00"},
		{ID: "4", TaskID: "C", MemberIDs: []string{}, Rejected: true, DecidedAt: "2026-09-03T09:00:00+07:00"},
	}, "2026-09-20T09:00:00+07:00")
	if stab.Stable != 2 || stab.Moved != 1 || stab.Pending != 0 {
		t.Fatalf("unexpected stability: %+v", stab)
	}
	approxEqual(t, stab.Rate, 2.0/3.0, 1e-6, "stability rate")

	acc := engine.ComputeEstimateAccuracy([]task.Task{
		{ID: "1", Status: task.StatusDone, Assignments: []task.Assignment{{MemberID: "m", Hours: 8}}, ActualHours: floatPtr(12)},
		{ID: "2", Status: task.StatusDone, Assignments: []task.Assignment{{MemberID: "m", Hours: 10}}, ActualHours: floatPtr(10)},
		{ID: "3", Status: task.StatusDone, Assignments: []task.Assignment{{MemberID: "m", Hours: 6}}},
	})
	if acc.N != 2 || acc.Ratio != 1.25 {
		t.Fatalf("unexpected accuracy: %+v", acc)
	}
}

func TestAtRiskAndGraphAndDeterministicDNA(t *testing.T) {
	ds, err := seeddata.LoadArunika()
	if err != nil {
		t.Fatalf("load seed: %v", err)
	}
	risky := engine.AtRiskCompetencies(ds.Competencies, ds.Members)
	if len(risky) == 0 {
		t.Fatalf("expected at-risk competencies in seed")
	}

	g := engine.BuildGraph(ds.Members, ds.Competencies, ds.Tasks, ds.Projects, 900, 560)
	if len(g.Nodes) == 0 || len(g.Edges) == 0 {
		t.Fatalf("expected non-empty graph")
	}

	dna := engine.AnalyzeDNADeterministic(
		"Migrasi Laravel dan Aplikasi Flutter",
		"Bangun Blade Laravel Eloquent yang aman untuk mobile app Flutter Dart.",
		task.PriorityHigh,
		ds.Competencies,
	)
	if len(dna.Requirements) == 0 {
		t.Fatalf("expected DNA requirements extracted")
	}
	foundLaravel, foundFlutter := false, false
	for _, r := range dna.Requirements {
		if r.CompetencyID == "laravel" {
			foundLaravel = true
		}
		if r.CompetencyID == "flutter" {
			foundFlutter = true
		}
		if r.Level != member.LevelIndep {
			t.Fatalf("expected Mandiri (2) level on 'aman/migrasi' keywords, got %d", r.Level)
		}
	}
	if !foundLaravel || !foundFlutter {
		t.Fatalf("expected both laravel and flutter extracted in DNA, got %+v", dna.Requirements)
	}
}
