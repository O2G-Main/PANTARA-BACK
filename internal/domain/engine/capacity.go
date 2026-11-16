package engine

import (
	"sort"
	"time"

	"github.com/Orang2-BU/pantara-back/internal/domain/member"
	"github.com/Orang2-BU/pantara-back/internal/domain/task"
)

const (
	WorkdaysPerWeek = 5.0
	LoadKnee        = 0.15
	DefaultOverrun  = 1.5
	MinSamples      = 5
)

// ReplanRisk flags an active task whose assignee cannot finish it on time.
type ReplanRisk struct {
	Task   task.Task     `json:"task"`
	Member member.Member `json:"member"`
	Kind   string        `json:"kind"` // "cuti" | "kurang-jam"
}

// WeekBucket tracks projected hours vs capacity for one Monday..Friday week.
type WeekBucket struct {
	Start    string  `json:"start"`
	Hours    float64 `json:"hours"`
	Capacity float64 `json:"capacity"`
}

// OverrunSummary summarizes historical actual/planned estimation drift.
type OverrunSummary struct {
	Factor  float64 `json:"factor"`
	Samples int     `json:"samples"`
}

// Workdays returns ISO dates (Mon-Fri) between from and to inclusive.
func Workdays(from, to string) []string {
	if from > to {
		return nil
	}
	start, err := time.Parse("2006-01-02", from)
	if err != nil {
		return nil
	}
	end, err := time.Parse("2006-01-02", to)
	if err != nil {
		return nil
	}
	var days []string
	for d := start.UTC(); !d.After(end.UTC()); d = d.AddDate(0, 0, 1) {
		wd := d.Weekday()
		if wd != time.Saturday && wd != time.Sunday {
			days = append(days, d.Format("2006-01-02"))
		}
	}
	return days
}

// HoursUntil returns non-leave capacity hours available to m between today and dueDate.
func HoursUntil(m member.Member, dueDate, today string) float64 {
	if m.AvailableHours == nil {
		return 0
	}
	leave := make(map[string]struct{}, len(m.LeaveDates))
	for _, d := range m.LeaveDates {
		leave[d] = struct{}{}
	}
	var openDays float64
	for _, d := range Workdays(today, dueDate) {
		if _, off := leave[d]; !off {
			openDays++
		}
	}
	return (*m.AvailableHours / WorkdaysPerWeek) * openDays
}

// CommittedUntil sums remaining hours on active tasks assigned to memberId due on or before dueDate.
func CommittedUntil(memberID, dueDate string, tasks []task.Task, excludeTaskID string) float64 {
	var total float64
	for _, t := range tasks {
		if !t.Status.IsActive() || t.ID == excludeTaskID || t.DueDate > dueDate {
			continue
		}
		var mine, totalAssigned float64
		for _, a := range t.Assignments {
			totalAssigned += a.Hours
			if a.MemberID == memberID {
				mine += a.Hours
			}
		}
		if mine > 0 && totalAssigned > 0 {
			total += t.RemainingHours * (mine / totalAssigned)
		}
	}
	return total
}

// LoadScore maps post-assignment utilization via a convex queue curve (1 at idle, 0 at >=100%).
func LoadScore(util float64) float64 {
	headroom := 1 - util
	if headroom <= 0 {
		return 0
	}
	v := ((1 + LoadKnee) * headroom) / (headroom + LoadKnee)
	if v > 1 {
		return 1
	}
	return v
}

// PlannedHours resolves planned hours from assignments first, falling back to DNA effort.
func PlannedHours(t task.Task) float64 {
	var sum float64
	for _, a := range t.Assignments {
		sum += a.Hours
	}
	if sum > 0 {
		return sum
	}
	if t.DNA != nil {
		return t.DNA.EffortHours
	}
	return 0
}

// OverrunFactor computes the 80th percentile actual/planned ratio when at least 5 done samples exist.
func OverrunFactor(tasks []task.Task) OverrunSummary {
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
	if len(ratios) < MinSamples {
		return OverrunSummary{Factor: DefaultOverrun, Samples: len(ratios)}
	}
	sort.Float64s(ratios)
	idx := int(float64(len(ratios))*0.8+0.999999999) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(ratios) {
		idx = len(ratios) - 1
	}
	f := ratios[idx]
	if f < 1 {
		f = 1
	}
	return OverrunSummary{Factor: Round2(f), Samples: len(ratios)}
}

// ReplanRisks finds active tasks whose assignee is out on leave or short of remaining hours before dueDate.
func ReplanRisks(tasks []task.Task, members []member.Member, today string) []ReplanRisk {
	byID := make(map[string]member.Member, len(members))
	for _, m := range members {
		byID[m.ID] = m
	}
	var risks []ReplanRisk
	for _, t := range tasks {
		if !t.Status.IsActive() || t.DueDate < today {
			continue
		}
		for _, a := range t.Assignments {
			m, ok := byID[a.MemberID]
			if !ok || m.AvailableHours == nil {
				continue
			}
			avail := HoursUntil(m, t.DueDate, today)
			if avail == 0 {
				risks = append(risks, ReplanRisk{Task: t, Member: m, Kind: "cuti"})
				continue
			}
			committed := CommittedUntil(m.ID, t.DueDate, tasks, "")
			if committed > avail+0.01 {
				risks = append(risks, ReplanRisk{Task: t, Member: m, Kind: "kurang-jam"})
			}
		}
	}
	return risks
}

func mondayOf(iso string) string {
	d, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return iso
	}
	wd := int(d.Weekday())
	shift := (wd + 6) % 7
	return d.UTC().AddDate(0, 0, -shift).Format("2006-01-02")
}

func addDaysISO(iso string, days int) string {
	d, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return iso
	}
	return d.UTC().AddDate(0, 0, days).Format("2006-01-02")
}

// WeeklyLoad distributes remaining active task hours evenly across workdays for 4-week projection.
func WeeklyLoad(m member.Member, tasks []task.Task, today string, weeks int) []WeekBucket {
	if weeks <= 0 {
		weeks = 4
	}
	leave := make(map[string]struct{}, len(m.LeaveDates))
	for _, d := range m.LeaveDates {
		leave[d] = struct{}{}
	}
	baseAvail := 0.0
	if m.AvailableHours != nil {
		baseAvail = *m.AvailableHours
	}
	perDay := baseAvail / WorkdaysPerWeek
	w0 := mondayOf(today)

	type bucket struct {
		start string
		hours float64
		cap   float64
	}
	buckets := make([]bucket, 0, weeks)
	dayToBucket := make(map[string]int, weeks*5)
	for i := 0; i < weeks; i++ {
		ws := addDaysISO(w0, i*7)
		we := addDaysISO(ws, 4)
		var weekCap float64
		for _, day := range Workdays(ws, we) {
			dayToBucket[day] = i
			if _, off := leave[day]; !off {
				weekCap += perDay
			}
		}
		buckets = append(buckets, bucket{start: ws, cap: weekCap})
	}

	for _, t := range tasks {
		if !t.Status.IsActive() {
			continue
		}
		var mine, total float64
		for _, a := range t.Assignments {
			total += a.Hours
			if a.MemberID == m.ID {
				mine += a.Hours
			}
		}
		if mine <= 0 || total <= 0 {
			continue
		}
		end := t.DueDate
		if end < today {
			end = today
		}
		days := Workdays(today, end)
		if len(days) == 0 {
			continue
		}
		each := (t.RemainingHours * (mine / total)) / float64(len(days))
		for _, day := range days {
			if idx, ok := dayToBucket[day]; ok {
				buckets[idx].hours += each
			}
		}
	}

	out := make([]WeekBucket, len(buckets))
	for i, b := range buckets {
		out[i] = WeekBucket{Start: b.start, Hours: Round2(b.hours), Capacity: Round2(b.cap)}
	}
	return out
}
