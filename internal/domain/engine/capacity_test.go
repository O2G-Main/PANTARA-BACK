package engine_test

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/Orang2-BU/pantara-back/internal/domain/engine"
	"github.com/Orang2-BU/pantara-back/internal/domain/member"
	"github.com/Orang2-BU/pantara-back/internal/domain/task"
)

func floatPtr(v float64) *float64 { return &v }

func approxEqual(t *testing.T, got, want, tol float64, label string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s: got %.6f, want %.6f (tol %.6f)", label, got, want, tol)
	}
}

func makeMember(avail *float64, leave []string) member.Member {
	return member.Member{
		ID:             "m",
		Name:           "Mira Lestari",
		Initials:       "ML",
		Role:           "Pengembang",
		Email:          "m@contoh.id",
		AvailableHours: avail,
		LeaveDates:     leave,
		WeeklyLimit:    40,
		Access:         "anggota",
	}
}

func makeTask(id, due string, rem float64, asgns []task.Assignment) task.Task {
	return task.Task{
		ID:             id,
		ProjectID:      "p",
		Title:          "Tugas",
		DueDate:        due,
		Priority:       task.PriorityNormal,
		Status:         task.StatusRunning,
		Assignments:    asgns,
		RemainingHours: rem,
		CreatedAt:      "2026-09-28T09:00:00+07:00",
	}
}

func TestWorkdays(t *testing.T) {
	got := engine.Workdays("2026-09-29", "2026-10-02")
	want := []string{"2026-09-29", "2026-09-30", "2026-10-01", "2026-10-02"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("workdays midweek: got %v, want %v", got, want)
	}

	gotWeekend := engine.Workdays("2026-10-02", "2026-10-06")
	wantWeekend := []string{"2026-10-02", "2026-10-05", "2026-10-06"}
	if !reflect.DeepEqual(gotWeekend, wantWeekend) {
		t.Fatalf("workdays weekend: got %v, want %v", gotWeekend, wantWeekend)
	}

	if gotEmpty := engine.Workdays("2026-10-02", "2026-09-30"); len(gotEmpty) != 0 {
		t.Fatalf("expected empty slice when end < start, got %v", gotEmpty)
	}
}

func TestHoursUntil(t *testing.T) {
	m := makeMember(floatPtr(32), nil)
	approxEqual(t, engine.HoursUntil(m, "2026-10-02", "2026-09-29"), 25.6, 1e-6, "base hoursUntil")

	mLeave := makeMember(floatPtr(32), []string{"2026-09-30"})
	approxEqual(t, engine.HoursUntil(mLeave, "2026-10-02", "2026-09-29"), 19.2, 1e-6, "leave reduces hoursUntil")

	mPastLeave := makeMember(floatPtr(32), []string{"2026-09-25"})
	approxEqual(t, engine.HoursUntil(mPastLeave, "2026-10-02", "2026-09-29"), 25.6, 1e-6, "past leave ignored")

	mAllLeave := makeMember(floatPtr(32), []string{"2026-09-29", "2026-09-30"})
	approxEqual(t, engine.HoursUntil(mAllLeave, "2026-09-30", "2026-09-29"), 0, 1e-6, "all leave")

	mNil := makeMember(nil, nil)
	approxEqual(t, engine.HoursUntil(mNil, "2026-10-02", "2026-09-29"), 0, 1e-6, "nil available")
}

func TestCommittedUntil(t *testing.T) {
	tasks := []task.Task{
		makeTask("A", "2026-10-01", 6, []task.Assignment{{MemberID: "m", Hours: 8}}),
		makeTask("B", "2026-10-02", 6, []task.Assignment{{MemberID: "m", Hours: 4}, {MemberID: "x", Hours: 4}}),
		makeTask("C", "2026-10-05", 9, []task.Assignment{{MemberID: "m", Hours: 8}}),
		{ID: "D", Status: task.StatusDone, RemainingHours: 0, Assignments: []task.Assignment{{MemberID: "m", Hours: 8}}, DueDate: "2026-10-01"},
	}
	approxEqual(t, engine.CommittedUntil("m", "2026-10-02", tasks, ""), 9, 1e-6, "committed all")
	approxEqual(t, engine.CommittedUntil("m", "2026-10-02", tasks, "A"), 3, 1e-6, "committed exclude A")
}

func TestLoadScore(t *testing.T) {
	approxEqual(t, engine.LoadScore(0), 1, 1e-6, "util 0")
	approxEqual(t, engine.LoadScore(1), 0, 1e-6, "util 1")
	approxEqual(t, engine.LoadScore(1.2), 0, 1e-6, "util 1.2")
	approxEqual(t, engine.LoadScore(0.85), 0.575, 1e-3, "util 0.85")

	loss := func(u float64) float64 { return 1 - engine.LoadScore(u) }
	if !(loss(0.9)-loss(0.75) > 3*(loss(0.15)-loss(0))) {
		t.Fatalf("expected convex queue load score")
	}
}

func TestOverrunFactor(t *testing.T) {
	doneOne := makeTask("1", "2026-10-01", 0, []task.Assignment{{MemberID: "m", Hours: 8}})
	doneOne.Status = task.StatusDone
	doneOne.ActualHours = floatPtr(12)
	res1 := engine.OverrunFactor([]task.Task{doneOne})
	if res1.Factor != 1.5 || res1.Samples != 1 {
		t.Fatalf("expected default 1.5 with 1 sample, got %+v", res1)
	}

	ratios := []float64{1, 1.2, 1.4, 1.6, 2}
	var doneFive []task.Task
	for i, r := range ratios {
		item := makeTask(string(rune('A'+i)), "2026-10-01", 0, []task.Assignment{{MemberID: "m", Hours: 10}})
		item.Status = task.StatusDone
		item.ActualHours = floatPtr(10 * r)
		doneFive = append(doneFive, item)
	}
	res5 := engine.OverrunFactor(doneFive)
	if res5.Factor != 1.6 || res5.Samples != 5 {
		t.Fatalf("expected p80 1.6 with 5 samples, got %+v", res5)
	}
}

func TestReplanRisksAndWeeklyLoad(t *testing.T) {
	tasks := []task.Task{
		makeTask("A", "2026-09-30", 4, []task.Assignment{{MemberID: "cuti", Hours: 4}}),
		makeTask("B", "2026-09-30", 20, []task.Assignment{{MemberID: "padat", Hours: 20}}),
		makeTask("C", "2026-10-02", 4, []task.Assignment{{MemberID: "aman", Hours: 4}}),
	}
	members := []member.Member{
		func() member.Member { m := makeMember(floatPtr(32), []string{"2026-09-29", "2026-09-30"}); m.ID = "cuti"; return m }(),
		func() member.Member { m := makeMember(floatPtr(32), nil); m.ID = "padat"; return m }(),
		func() member.Member { m := makeMember(floatPtr(32), nil); m.ID = "aman"; return m }(),
	}
	risks := engine.ReplanRisks(tasks, members, "2026-09-29")
	if len(risks) != 2 || risks[0].Task.ID != "A" || risks[0].Kind != "cuti" || risks[1].Task.ID != "B" || risks[1].Kind != "kurang-jam" {
		t.Fatalf("unexpected replan risks: %+v", risks)
	}

	load := engine.WeeklyLoad(members[2], tasks, "2026-09-29", 4)
	if len(load) != 4 || load[0].Hours != 4 {
		t.Fatalf("unexpected weekly load: %+v", load)
	}
}

func TestExtraTimeLifecycle(t *testing.T) {
	due := makeTask("T1", "2026-10-02", 6, []task.Assignment{{MemberID: "m", Hours: 8}, {MemberID: "x", Hours: 4}})
	due.DueTime = "15:00"

	dl, err := engine.DeadlineAt(due.DueDate, due.DueTime)
	if err != nil || dl.UTC().Format(time.RFC3339) != "2026-10-02T08:00:00Z" {
		t.Fatalf("unexpected deadlineAt: %v %v", dl, err)
	}
	dlDefault, err := engine.DeadlineAt("2026-10-02", "")
	if err != nil || dlDefault.UTC().Format(time.RFC3339) != "2026-10-02T10:00:00Z" {
		t.Fatalf("unexpected default deadlineAt: %v %v", dlDefault, err)
	}

	tEarly, _ := time.Parse(time.RFC3339, "2026-10-02T12:59:00+07:00")
	tExact, _ := time.Parse(time.RFC3339, "2026-10-02T13:00:00+07:00")
	tLate, _ := time.Parse(time.RFC3339, "2026-10-02T16:00:00+07:00")

	if engine.CanRequestExtraTime(due, tEarly) {
		t.Fatalf("expected CanRequestExtraTime false 2h01m before deadline")
	}
	if !engine.CanRequestExtraTime(due, tExact) || !engine.CanRequestExtraTime(due, tLate) {
		t.Fatalf("expected CanRequestExtraTime true within <=2h window")
	}

	pending := due
	pending.ExtraTime = &task.ExtraTimeRequest{MemberID: "m", Hours: 3, Reason: "Perlu tambahan skenario uji.", Status: task.ExtraTimePending}
	if engine.CanRequestExtraTime(pending, tExact) {
		t.Fatalf("must block when request is already pending")
	}

	okTask, ok := engine.DecideExtraTime(pending, true)
	if !ok || okTask.RemainingHours != 9 || okTask.Assignments[0].Hours != 11 || okTask.Assignments[1].Hours != 4 || okTask.ExtraTime.Status != task.ExtraTimeApproved {
		t.Fatalf("unexpected approve result: ok=%v task=%+v", ok, okTask)
	}
	rejTask, ok := engine.DecideExtraTime(pending, false)
	if !ok || rejTask.RemainingHours != 6 || rejTask.ExtraTime.Status != task.ExtraTimeRejected {
		t.Fatalf("unexpected reject result: ok=%v task=%+v", ok, rejTask)
	}
	if _, ok := engine.DecideExtraTime(okTask, false); ok {
		t.Fatalf("decided request must not be decided again")
	}
}

func TestLeaveValidationAndParallelApproval(t *testing.T) {
	today := "2026-09-29"
	if msg := engine.ValidateLeaveRequest("", "2026-10-01", "Acara keluarga besar", today); msg == "" {
		t.Fatalf("expected error on missing start date")
	}
	if msg := engine.ValidateLeaveRequest("2026-09-28", "2026-10-01", "Acara keluarga besar", today); msg == "" {
		t.Fatalf("expected error when start < today")
	}
	if msg := engine.ValidateLeaveRequest("2026-10-02", "2026-10-01", "Acara keluarga besar", today); msg == "" {
		t.Fatalf("expected error when end < start")
	}
	if msg := engine.ValidateLeaveRequest("2026-10-03", "2026-10-04", "Acara keluarga besar", today); msg == "" {
		t.Fatalf("expected error on weekend-only leave")
	}
	if msg := engine.ValidateLeaveRequest("2026-10-01", "2026-10-02", "Sakit", today); msg == "" {
		t.Fatalf("expected error on short reason")
	}
	if msg := engine.ValidateLeaveRequest("2026-10-01", "2026-10-02", "Acara keluarga di luar kota", today); msg != "" {
		t.Fatalf("unexpected validation error: %s", msg)
	}

	req := member.LeaveRequest{
		ID:      "c1",
		Start:   "2026-10-01",
		End:     "2026-10-05",
		Type:    member.LeaveAnnual,
		Reason:  "Acara keluarga di luar kota",
		Manager: member.ApprovalPending,
		HR:      member.ApprovalPending,
	}
	m := makeMember(floatPtr(32), []string{"2026-09-30"})
	m.LeaveRequests = []member.LeaveRequest{req}

	mMgr, _, ok := engine.DecideLeave(m, "c1", "manager", true)
	if !ok || len(mMgr.LeaveDates) != 1 {
		t.Fatalf("manager-only approval must not modify LeaveDates yet")
	}
	mBoth, _, ok := engine.DecideLeave(mMgr, "c1", "hr", true)
	if !ok {
		t.Fatalf("expected second HR approval to succeed")
	}
	wantDates := []string{"2026-09-30", "2026-10-01", "2026-10-02", "2026-10-05"}
	if !reflect.DeepEqual(mBoth.LeaveDates, wantDates) {
		t.Fatalf("expected merged leaveDates %v, got %v", wantDates, mBoth.LeaveDates)
	}

	mRej, _, ok := engine.DecideLeave(m, "c1", "manager", false)
	if !ok || mRej.LeaveRequests[0].CombinedStatus() != member.ApprovalRejected {
		t.Fatalf("expected combined rejected status")
	}
	if _, _, ok := engine.DecideLeave(mRej, "c1", "hr", true); ok {
		t.Fatalf("rejected leave request must not allow further approval")
	}
}

func TestAutomaticPerformance(t *testing.T) {
	q80 := 80.0
	q60 := 60.0
	tasks := []task.Task{
		{ID: "1", DueDate: "2026-09-20", Status: task.StatusDone, CompletedAt: "2026-09-20", QualityScore: &q80, Assignments: []task.Assignment{{MemberID: "a", Hours: 4}}},
		{ID: "2", DueDate: "2026-09-20", Status: task.StatusDone, CompletedAt: "2026-09-21", QualityScore: &q60, Assignments: []task.Assignment{{MemberID: "a", Hours: 4}}},
		{ID: "3", DueDate: "2026-10-01", Status: task.StatusDone, CompletedAt: "2026-10-01", QualityScore: &q80, Assignments: []task.Assignment{{MemberID: "a", Hours: 4}}},
	}
	ot, qual, sc := engine.AutomaticPerformance(tasks, "a", "2026-09")
	if ot == nil || qual == nil || sc == nil || *ot != 50 || *qual != 70 || *sc != 60 {
		t.Fatalf("unexpected automatic performance: ot=%v qual=%v sc=%v", ot, qual, sc)
	}
	tasksMissingQual := []task.Task{
		{ID: "1", DueDate: "2026-09-20", Status: task.StatusDone, CompletedAt: "2026-09-20", QualityScore: nil, Assignments: []task.Assignment{{MemberID: "a", Hours: 4}}},
	}
	ot2, qual2, sc2 := engine.AutomaticPerformance(tasksMissingQual, "a", "2026-09")
	if ot2 == nil || qual2 != nil || sc2 != nil {
		t.Fatalf("expected nil quality/score when quality missing, got ot=%v qual=%v sc=%v", ot2, qual2, sc2)
	}
}
