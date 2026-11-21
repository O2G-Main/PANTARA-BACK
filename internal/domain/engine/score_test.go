package engine_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Orang2-BU/pantara-back/internal/domain/engine"
	"github.com/Orang2-BU/pantara-back/internal/domain/member"
	"github.com/Orang2-BU/pantara-back/internal/domain/task"
	"github.com/Orang2-BU/pantara-back/internal/domain/workspace"
	"github.com/Orang2-BU/pantara-back/internal/seeddata"
)

const testToday = "2026-09-29"

func loadSeedEngineFixture(t *testing.T) (seeddata.Dataset, task.Task, map[string]string) {
	t.Helper()
	ds, err := seeddata.LoadArunika()
	if err != nil {
		t.Fatalf("load arunika seed: %v", err)
	}
	names := make(map[string]string, len(ds.Competencies))
	for _, c := range ds.Competencies {
		names[c.ID] = c.Name
	}
	var auth task.Task
	for _, item := range ds.Tasks {
		if item.ID == "TGS-0142" {
			auth = item
			break
		}
	}
	if auth.DNA == nil {
		t.Fatalf("expected TGS-0142 with DNA")
	}
	dnaCopy := *auth.DNA
	dnaCopy.EffortHours = 8
	dnaCopy.Confirmed = true
	auth.DNA = &dnaCopy
	return ds, auth, names
}

func TestSeedEngineParity(t *testing.T) {
	ds, authCorrected, names := loadSeedEngineFixture(t)

	if len(ds.Members) != 20 {
		t.Fatalf("expected 20 members, got %d", len(ds.Members))
	}

	gotAlloc := []float64{
		engine.AllocatedHours("ayu", ds.Tasks),
		engine.AllocatedHours("bima", ds.Tasks),
		engine.AllocatedHours("dimas", ds.Tasks),
		engine.AllocatedHours("citra", ds.Tasks),
		engine.AllocatedHours("andre", ds.Tasks),
		engine.AllocatedHours("bayu", ds.Tasks),
	}
	wantAlloc := []float64{28, 16, 22, 30, 30, 0}
	if !reflect.DeepEqual(gotAlloc, wantAlloc) {
		t.Fatalf("allocated hours: got %v, want %v", gotAlloc, wantAlloc)
	}

	if engine.ClashingDeadline("ayu", ds.Tasks) != "2026-09-30" {
		t.Fatalf("expected ayu clashing deadline 2026-09-30")
	}
	if engine.ClashingDeadline("citra", ds.Tasks) != "2026-10-01" {
		t.Fatalf("expected citra clashing deadline 2026-10-01")
	}
	if engine.ClashingDeadline("bima", ds.Tasks) != "" {
		t.Fatalf("expected bima no clashing deadline")
	}

	settings := workspace.DefaultEngineSettings()
	res := engine.RankCandidates(authCorrected, ds.Members, ds.Tasks, settings, names, testToday)

	ids := make([]string, len(res.Candidates))
	for i, c := range res.Candidates {
		ids[i] = c.Member.ID
	}
	if !reflect.DeepEqual(ids, []string{"bima", "ayu", "dimas"}) {
		t.Fatalf("ranked candidates: got %v, want [bima ayu dimas]", ids)
	}

	// Score verification for Bima matching engine.test.ts:52-53
	k := (0.96 + 0.92*0.9 + 0.66*0.9) / 2.54
	wantBima := 0.45*k + 0.2*(1.15*0.25/0.4) + 0.15 + 0.1 - 0.1*0.5
	approxEqual(t, res.Candidates[0].Score, wantBima, 1e-6, "Bima exact score")

	// Check Citra failure message with competency names
	var citraFail []string
	for _, f := range res.Filtered {
		if f.Member.ID == "citra" {
			citraFail = f.Failures
		}
	}
	foundCitraMsg := false
	for _, msg := range citraFail {
		if msg == "Backend API belum dimiliki, butuh minimal Dasar" {
			foundCitraMsg = true
		}
	}
	if !foundCitraMsg {
		t.Fatalf("expected citra missing Backend API failure, got %v", citraFail)
	}

	// Preset sebar swaps dimas above ayu
	sebarSettings := settings
	sebarSettings.Preset = workspace.PresetSebar
	resSebar := engine.RankCandidates(authCorrected, ds.Members, ds.Tasks, sebarSettings, names, testToday)
	sebarIDs := make([]string, len(resSebar.Candidates))
	for i, c := range resSebar.Candidates {
		sebarIDs[i] = c.Member.ID
	}
	if !reflect.DeepEqual(sebarIDs, []string{"bima", "dimas", "ayu"}) {
		t.Fatalf("sebar candidates: got %v, want [bima dimas ayu]", sebarIDs)
	}

	// Overload check for Ayu
	ayu := res.Candidates[1]
	approxEqual(t, ayu.UtilAfter, 1.125, 1e-6, "Ayu utilAfter")
	if ayu.Factors.C != 0 || !strings.Contains(strings.Join(ayu.Cautions, " "), "melebihi kapasitas") {
		t.Fatalf("expected Ayu load 0 and overload caution")
	}

	// Dimas overrun warning
	dimas := res.Candidates[2]
	if dimas.UtilAfter >= 1 || dimas.UtilAfterHigh < 1 || !strings.Contains(strings.Join(dimas.Cautions, " "), "molor") {
		t.Fatalf("expected Dimas overrun caution")
	}

	// Simulate check without mutation
	sim := engine.Simulate(authCorrected, []task.Assignment{{MemberID: "ayu", Hours: 8}}, ds.Members, ds.Tasks)
	hasCrit, hasNear, hasDep := false, false, false
	for _, c := range sim.Conflicts {
		if c.Tone == "crit" {
			hasCrit = true
		}
		if strings.Contains(c.Text, "berdekatan") {
			hasNear = true
		}
		if strings.Contains(c.Text, "Bergantung") {
			hasDep = true
		}
	}
	if !hasCrit || !hasNear || !hasDep {
		t.Fatalf("expected crit, nearby deadline, and dependency conflicts, got %+v", sim.Conflicts)
	}

	// Batch allocation parity & coverage
	var openTasks []task.Task
	for _, item := range ds.Tasks {
		if item.Status == task.StatusOpen {
			openTasks = append(openTasks, item)
		}
	}
	pairs := engine.AllocateBatch(openTasks, ds.Members, ds.Tasks, settings, names, testToday)
	if len(pairs) != len(openTasks) {
		t.Fatalf("expected %d batch pairs, got %d", len(openTasks), len(pairs))
	}
	if pairs[0].Task.ID != "TGS-0142" {
		t.Fatalf("expected TGS-0142 first in batch, got %s", pairs[0].Task.ID)
	}
	for _, p := range pairs {
		if p.MemberID != nil && *p.MemberID == "bayu" {
			t.Fatalf("bayu with zero competencies must never be assigned")
		}
		if p.Task.ID == "TGS-0168" {
			if p.MemberID != nil || !strings.Contains(p.Reason, "Tidak ada yang lolos") {
				t.Fatalf("expected TGS-0168 unassignable with reason, got %+v", p)
			}
		}
	}
}

func TestRankCandidatesEdgeCases(t *testing.T) {
	ds, authCorrected, names := loadSeedEngineFixture(t)
	settings := workspace.DefaultEngineSettings()

	// All workdays on leave filters Bima
	membersLeaveAll := append([]member.Member(nil), ds.Members...)
	for i := range membersLeaveAll {
		if membersLeaveAll[i].ID == "bima" {
			membersLeaveAll[i].LeaveDates = []string{"2026-09-29", "2026-09-30", "2026-10-01", "2026-10-02"}
		}
	}
	resLeave := engine.RankCandidates(authCorrected, membersLeaveAll, ds.Tasks, settings, names, testToday)
	foundLeave := false
	for _, f := range resLeave.Filtered {
		if f.Member.ID == "bima" && strings.Contains(strings.Join(f.Failures, " "), "hari kerja") {
			foundLeave = true
		}
	}
	if !foundLeave {
		t.Fatalf("expected Bima filtered when leaving all workdays")
	}

	// TiedWithPrev check
	var bima member.Member
	for _, m := range ds.Members {
		if m.ID == "bima" {
			bima = m
		}
	}
	twin := bima
	twin.ID = "bima-2"
	twin.Name = "Bima Kembar"
	resTwin := engine.RankCandidates(authCorrected, []member.Member{bima, twin}, ds.Tasks, settings, names, testToday)
	if len(resTwin.Candidates) != 2 || resTwin.Candidates[0].TiedWithPrev || !resTwin.Candidates[1].TiedWithPrev {
		t.Fatalf("expected second twin tiedWithPrev=true, got %+v", resTwin.Candidates)
	}
}

func TestReserveRatioCapacity(t *testing.T) {
	ds, authCorrected, names := loadSeedEngineFixture(t)
	findBima := func(res engine.RankResult) engine.Candidate {
		for _, c := range res.Candidates {
			if c.Member.ID == "bima" {
				return c
			}
		}
		t.Fatalf("bima not found in candidates")
		return engine.Candidate{}
	}

	baseSettings := workspace.DefaultEngineSettings()
	reserveSettings := baseSettings
	reserveSettings.ReserveRatio = 0.2

	normalTask := authCorrected
	normalTask.Priority = task.PriorityNormal
	bimaBase := findBima(engine.RankCandidates(normalTask, ds.Members, ds.Tasks, baseSettings, names, testToday))
	bimaReserve := findBima(engine.RankCandidates(normalTask, ds.Members, ds.Tasks, reserveSettings, names, testToday))
	if !(bimaReserve.UtilAfter > bimaBase.UtilAfter && bimaReserve.Free < bimaBase.Free) {
		t.Fatalf("expected reserveRatio to reduce open capacity for normal tasks")
	}

	urgentTask := authCorrected
	urgentTask.Priority = task.PriorityHigh
	bimaUrgentBase := findBima(engine.RankCandidates(urgentTask, ds.Members, ds.Tasks, baseSettings, names, testToday))
	bimaUrgentReserve := findBima(engine.RankCandidates(urgentTask, ds.Members, ds.Tasks, reserveSettings, names, testToday))
	approxEqual(t, bimaUrgentReserve.UtilAfter, bimaUrgentBase.UtilAfter, 1e-6, "urgent task bypasses reserveRatio")
}
