package schedule

import (
	"errors"
	"testing"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

func mustLoad(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return loc
}

func TestNextRunOneTime(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour)
	s := domain.Schedule{Kind: domain.ScheduleOneTime, RunAt: &future, Timezone: "UTC"}
	if got := NextRun(s, now); got == nil || !got.Equal(future) {
		t.Fatalf("future one-time: want %v, got %v", future, got)
	}
	past := now.Add(-time.Hour)
	s.RunAt = &past
	if got := NextRun(s, now); got != nil {
		t.Fatalf("past one-time: want nil, got %v", got)
	}
	s.RunAt = &now
	if got := NextRun(s, now); got != nil {
		t.Fatalf("equal one-time: want nil (strictly after), got %v", got)
	}
	s.RunAt = nil
	if got := NextRun(s, now); got != nil {
		t.Fatalf("nil run_at: want nil, got %v", got)
	}
}

func TestNextRunHourly(t *testing.T) {
	loc := time.UTC
	cases := []struct {
		name     string
		interval int
		after    time.Time
		want     time.Time
	}{
		{"6h grid", 6, time.Date(2026, 5, 1, 10, 15, 0, 0, loc), time.Date(2026, 5, 1, 12, 0, 0, 0, loc)},
		{"exact grid advances", 1, time.Date(2026, 5, 1, 10, 0, 0, 0, loc), time.Date(2026, 5, 1, 11, 0, 0, 0, loc)},
		{"sub-second after grid", 1, time.Date(2026, 5, 1, 10, 0, 0, 1, loc), time.Date(2026, 5, 1, 11, 0, 0, 0, loc)},
		{"across midnight", 8, time.Date(2026, 5, 1, 22, 0, 0, 0, loc), time.Date(2026, 5, 2, 0, 0, 0, 0, loc)},
		{"odd interval", 5, time.Date(2026, 5, 1, 23, 30, 0, 0, loc), time.Date(2026, 5, 2, 1, 0, 0, 0, loc)},
	}
	for _, tc := range cases {
		s := domain.Schedule{Kind: domain.ScheduleHourly, IntervalHours: tc.interval, Timezone: "UTC"}
		got := NextRun(s, tc.after)
		if got == nil || !got.Equal(tc.want) {
			t.Errorf("%s: want %v, got %v", tc.name, tc.want, got)
		}
	}
	s := domain.Schedule{Kind: domain.ScheduleHourly, IntervalHours: 0, Timezone: "UTC"}
	if got := NextRun(s, time.Now()); got != nil {
		t.Errorf("zero interval: want nil, got %v", got)
	}
}

func TestNextRunDailyJakarta(t *testing.T) {
	loc := mustLoad(t, "Asia/Jakarta")
	after := time.Date(2026, 5, 1, 10, 0, 0, 0, loc)
	s := domain.Schedule{Kind: domain.ScheduleDaily, TimeOfDay: "02:30", Timezone: "Asia/Jakarta"}
	want := time.Date(2026, 5, 2, 2, 30, 0, 0, loc)
	if got := NextRun(s, after); got == nil || !got.Equal(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	// Same morning before the firing time: today.
	after = time.Date(2026, 5, 1, 1, 0, 0, 0, loc)
	want = time.Date(2026, 5, 1, 2, 30, 0, 0, loc)
	if got := NextRun(s, after); got == nil || !got.Equal(want) {
		t.Fatalf("same-day: want %v, got %v", want, got)
	}
	// Exactly at firing time: tomorrow (strictly after).
	after = want
	want = time.Date(2026, 5, 2, 2, 30, 0, 0, loc)
	if got := NextRun(s, after); got == nil || !got.Equal(want) {
		t.Fatalf("exact: want %v, got %v", want, got)
	}
}

func TestNextRunDailySpringForwardGap(t *testing.T) {
	ny := mustLoad(t, "America/New_York")
	// 2026-03-08: clocks jump 02:00 -> 03:00; 02:30 does not exist.
	after := time.Date(2026, 3, 8, 0, 0, 0, 0, ny)
	s := domain.Schedule{Kind: domain.ScheduleDaily, TimeOfDay: "02:30", Timezone: "America/New_York"}
	got := NextRun(s, after)
	if got == nil {
		t.Fatal("gap day must still produce a firing, got nil")
	}
	if !got.After(after) {
		t.Fatalf("must be strictly after: after=%v got=%v", after, got)
	}
	y, m, d := got.In(ny).Date()
	if y != 2026 || m != time.March || d != 8 {
		t.Fatalf("gap-day firing must land on Mar 8, got %v", got.In(ny))
	}
	t.Logf("gap 02:30 resolves to wall %s", got.In(ny).Format("15:04 MST"))
	// The day after the gap resumes the normal wall time (no skip, no dup).
	nxt := NextRun(s, *got)
	wantNext := time.Date(2026, 3, 9, 2, 30, 0, 0, ny)
	if nxt == nil || !nxt.Equal(wantNext) {
		t.Fatalf("post-gap: want %v, got %v", wantNext, nxt)
	}
}

func TestNextRunDailyFallBackOverlap(t *testing.T) {
	ny := mustLoad(t, "America/New_York")
	// 2026-11-01: 01:00-02:00 happens twice; first occurrence wins.
	after := time.Date(2026, 11, 1, 0, 30, 0, 0, ny)
	s := domain.Schedule{Kind: domain.ScheduleDaily, TimeOfDay: "01:30", Timezone: "America/New_York"}
	got := NextRun(s, after)
	if got == nil || !got.After(after) {
		t.Fatalf("want firing after %v, got %v", after, got)
	}
	if h, m, _ := got.In(ny).Clock(); h != 1 || m != 30 {
		t.Fatalf("want 01:30 wall, got %s", got.In(ny).Format("15:04 MST"))
	}
	// Hourly grid must not double-fire across the fallback: successive
	// hourly runs stay strictly increasing with no duplicates.
	h := domain.Schedule{Kind: domain.ScheduleHourly, IntervalHours: 1, Timezone: "America/New_York"}
	seen := map[int64]bool{}
	cur := time.Date(2026, 11, 1, 0, 0, 0, 0, ny)
	for i := 0; i < 6; i++ {
		nxt := NextRun(h, cur)
		if nxt == nil || !nxt.After(cur) {
			t.Fatalf("hourly step %d: want after %v, got %v", i, cur, nxt)
		}
		if seen[nxt.Unix()] {
			t.Fatalf("hourly double-fire at %v", nxt)
		}
		seen[nxt.Unix()] = true
		cur = *nxt
	}
}

func TestNextRunWeekly(t *testing.T) {
	wd := time.Wednesday
	s := domain.Schedule{Kind: domain.ScheduleWeekly, TimeOfDay: "03:00", Weekday: &wd, Timezone: "UTC"}
	// 2026-05-05 is a Tuesday.
	after := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	want := time.Date(2026, 5, 6, 3, 0, 0, 0, time.UTC)
	if got := NextRun(s, after); got == nil || !got.Equal(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	// Same Wednesday before firing time: today.
	after = time.Date(2026, 5, 6, 2, 0, 0, 0, time.UTC)
	want = time.Date(2026, 5, 6, 3, 0, 0, 0, time.UTC)
	if got := NextRun(s, after); got == nil || !got.Equal(want) {
		t.Fatalf("same-day: want %v, got %v", want, got)
	}
	// Same Wednesday after firing time: next week.
	after = time.Date(2026, 5, 6, 4, 0, 0, 0, time.UTC)
	want = time.Date(2026, 5, 13, 3, 0, 0, 0, time.UTC)
	if got := NextRun(s, after); got == nil || !got.Equal(want) {
		t.Fatalf("next-week: want %v, got %v", want, got)
	}
	s.Weekday = nil
	if got := NextRun(s, after); got != nil {
		t.Fatalf("nil weekday: want nil, got %v", got)
	}
}

func TestNextRunDisabledAndBadTimezone(t *testing.T) {
	s := domain.Schedule{Kind: domain.ScheduleDisabled, Timezone: "UTC"}
	if got := NextRun(s, time.Now()); got != nil {
		t.Fatalf("disabled: want nil, got %v", got)
	}
	s = domain.Schedule{Kind: domain.ScheduleDaily, TimeOfDay: "01:00", Timezone: "Mars/Olympus"}
	if got := NextRun(s, time.Now()); got != nil {
		t.Fatalf("bad timezone: want nil, got %v", got)
	}
}

func TestParseCron(t *testing.T) {
	valid := []string{
		"0 2 * * *",
		"*/15 * * * *",
		"0 9 * * mon",
		"0 0 1 jan *",
		"30 4 1-15/2 * sun,sat",
		"0 0 * * 7",     // 7 = Sunday
		"  0 2 * * *  ", // surrounding whitespace tolerated
	}
	for _, e := range valid {
		if _, err := ParseCron(e); err != nil {
			t.Errorf("valid %q rejected: %v", e, err)
		}
	}
	// 7 normalizes to Sunday.
	spec, _ := ParseCron("0 0 * * 7")
	if !spec.Weekdays[0] || spec.Weekdays[7] {
		t.Errorf("dow 7 must normalize to 0: %v", spec.Weekdays)
	}
	invalid := []string{
		"", "0 2 *", "* * * * * *", "61 * * * *", "* 24 * * *",
		"* * 0 * *", "* * 32 * *", "* * * 13 *", "* * * * 8",
		"*/0 * * * *", "1-0 * * * *", "nope * * * *",
		"0 0 * *", "5/ * * * *", "1,,2 * * * *",
	}
	for _, e := range invalid {
		if _, err := ParseCron(e); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("invalid %q: want ErrValidation, got %v", e, err)
		}
	}
}

func TestCronMatchesDomDOW(t *testing.T) {
	// Restricted dom OR restricted dow (Vixie semantics).
	spec, err := ParseCron("0 0 1 * fri")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	fridayMay1 := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC) // Friday AND 1st
	if !spec.Matches(fridayMay1) {
		t.Fatal("May 1 2026 (Fri + 1st) must match")
	}
	fridayMay8 := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC) // Friday, not 1st
	if !spec.Matches(fridayMay8) {
		t.Fatal("a Friday must match via dow")
	}
	mondayJun1 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) // 1st, not Friday
	if !spec.Matches(mondayJun1) {
		t.Fatal("the 1st must match via dom")
	}
	tuesdayMay5 := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
	if spec.Matches(tuesdayMay5) {
		t.Fatal("ordinary Tuesday must not match")
	}
	if spec.Matches(tuesdayMay5.Add(time.Hour)) {
		t.Fatal("wrong hour must not match")
	}
}

func TestNextCronAfter(t *testing.T) {
	loc := time.UTC
	spec, _ := ParseCron("*/15 * * * *")
	after := time.Date(2026, 5, 1, 10, 7, 0, 0, loc)
	want := time.Date(2026, 5, 1, 10, 15, 0, 0, loc)
	if got := NextCronAfter(spec, after, loc); got == nil || !got.Equal(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	// Exact boundary advances (strictly after).
	after = time.Date(2026, 5, 1, 10, 15, 0, 0, loc)
	want = time.Date(2026, 5, 1, 10, 30, 0, 0, loc)
	if got := NextCronAfter(spec, after, loc); got == nil || !got.Equal(want) {
		t.Fatalf("boundary: want %v, got %v", want, got)
	}
	// Weekly cron.
	mon9, _ := ParseCron("0 9 * * mon")
	after = time.Date(2026, 5, 5, 12, 0, 0, 0, loc) // Tuesday
	want = time.Date(2026, 5, 11, 9, 0, 0, 0, loc)
	if got := NextCronAfter(mon9, after, loc); got == nil || !got.Equal(want) {
		t.Fatalf("monday: want %v, got %v", want, got)
	}
	// Impossible date (Feb 30) yields nil instead of hanging.
	feb30, err := ParseCron("0 0 30 2 *")
	if err != nil {
		t.Fatalf("Feb 30 parses (validation is per-field): %v", err)
	}
	if got := NextCronAfter(feb30, after, loc); got != nil {
		t.Fatalf("Feb 30: want nil, got %v", got)
	}
	// Timezone-aware: 09:00 Jakarta is 02:00 UTC.
	jak := mustLoad(t, "Asia/Jakarta")
	daily9, _ := ParseCron("0 9 * * *")
	after = time.Date(2026, 5, 1, 1, 0, 0, 0, time.UTC) // 08:00 Jakarta
	want = time.Date(2026, 5, 1, 9, 0, 0, 0, jak)
	if got := NextCronAfter(daily9, after, jak); got == nil || !got.Equal(want) {
		t.Fatalf("jakarta: want %v, got %v", want, got)
	}
}
