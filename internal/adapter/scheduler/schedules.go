package scheduler

import (
	"context"
	"fmt"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// ScheduleSource lists due schedules for dispatch.
//
// INTEGRATION NOTE (repos track): domain.ScheduleRepository.DueSchedules has
// exactly this signature, so the generated repository satisfies this
// interface directly with no adapter.
type ScheduleSource interface {
	DueSchedules(ctx context.Context, now time.Time, limit int) ([]domain.Schedule, error)
}

// PeriodicSpec is one recurring-schedule definition derived from a schedule
// entity. Integrator-2 registers these as River periodic jobs
// (river.NewPeriodicJob); until then CollectDue + NextRun drive the
// in-process polling loop in serve.go.
type PeriodicSpec struct {
	ScheduleID string
	PolicyID   string
	// Interval is set for hourly schedules (every N hours).
	Interval time.Duration
	// Cron is a 5-field expression for daily/weekly schedules, evaluated in
	// Timezone.
	Cron     string
	Timezone string
}

// PeriodicSpecs converts active hourly/daily/weekly schedule entities into
// recurring definitions. One-time, disabled, and paused schedules are
// skipped (no error); cron-kind schedules pass their expression through.
func PeriodicSpecs(schedules []domain.Schedule) ([]PeriodicSpec, error) {
	var out []PeriodicSpec
	for _, s := range schedules {
		if !s.Active() || s.Kind == domain.ScheduleOneTime {
			continue
		}
		spec := PeriodicSpec{ScheduleID: s.ID, PolicyID: s.PolicyID, Timezone: s.Timezone}
		switch s.Kind {
		case domain.ScheduleHourly:
			if s.IntervalHours <= 0 {
				return nil, fmt.Errorf("scheduler: schedule %s: hourly interval must be positive", s.ID)
			}
			spec.Interval = time.Duration(s.IntervalHours) * time.Hour
		case domain.ScheduleDaily:
			min, hour, err := splitTimeOfDay(s.TimeOfDay)
			if err != nil {
				return nil, fmt.Errorf("scheduler: schedule %s: %w", s.ID, err)
			}
			spec.Cron = fmt.Sprintf("%d %d * * *", min, hour)
		case domain.ScheduleWeekly:
			if s.Weekday == nil {
				return nil, fmt.Errorf("scheduler: schedule %s: weekly schedule requires a weekday", s.ID)
			}
			min, hour, err := splitTimeOfDay(s.TimeOfDay)
			if err != nil {
				return nil, fmt.Errorf("scheduler: schedule %s: %w", s.ID, err)
			}
			spec.Cron = fmt.Sprintf("%d %d * * %d", min, hour, int(*s.Weekday))
		case domain.ScheduleCron:
			spec.Cron = s.CronExpr
		default:
			return nil, fmt.Errorf("scheduler: schedule %s: unknown kind %q", s.ID, s.Kind)
		}
		out = append(out, spec)
	}
	return out, nil
}

func splitTimeOfDay(v string) (min, hour int, err error) {
	t, err := time.Parse("15:04", v)
	if err != nil {
		return 0, 0, fmt.Errorf("time_of_day must be HH:MM, got %q", v)
	}
	return t.Minute(), t.Hour(), nil
}

// NextRun computes the first occurrence of s strictly after `after`.
// It supports one-time, hourly, daily, and weekly kinds; cron-kind schedules
// report ok=false (cron evaluation rides on River periodic jobs at
// integration — see PeriodicSpecs). Paused/disabled schedules report
// ok=false. All wall-clock math runs in the schedule's timezone.
func NextRun(s domain.Schedule, after time.Time) (next time.Time, ok bool) {
	if !s.Active() {
		return time.Time{}, false
	}
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		loc = time.UTC
	}
	after = after.In(loc)
	switch s.Kind {
	case domain.ScheduleOneTime:
		if s.RunAt == nil || !s.RunAt.After(after) {
			return time.Time{}, false
		}
		return s.RunAt.In(loc), true
	case domain.ScheduleHourly:
		if s.IntervalHours <= 0 {
			return time.Time{}, false
		}
		step := time.Duration(s.IntervalHours) * time.Hour
		// Anchor hourly grids at the Unix epoch so occurrences are stable
		// regardless of when the scheduler starts.
		n := after.Unix()/int64(step.Seconds()) + 1
		return time.Unix(n*int64(step.Seconds()), 0).In(loc), true
	case domain.ScheduleDaily, domain.ScheduleWeekly:
		tod, err := time.Parse("15:04", s.TimeOfDay)
		if err != nil {
			return time.Time{}, false
		}
		candidate := time.Date(after.Year(), after.Month(), after.Day(),
			tod.Hour(), tod.Minute(), 0, 0, loc)
		if s.Kind == domain.ScheduleDaily {
			if !candidate.After(after) {
				candidate = candidate.AddDate(0, 0, 1)
			}
			return candidate, true
		}
		if s.Weekday == nil {
			return time.Time{}, false
		}
		for i := 0; i < 8; i++ {
			if candidate.Weekday() == *s.Weekday && candidate.After(after) {
				return candidate, true
			}
			candidate = candidate.AddDate(0, 0, 1)
		}
		return time.Time{}, false
	default:
		return time.Time{}, false
	}
}

// Advance moves a schedule's NextRunAt to its next occurrence after now. It
// returns false when the schedule has no future occurrence (one-time in the
// past, paused, cron kind). Callers persist via domain.ScheduleRepository.
func Advance(s *domain.Schedule, now time.Time) bool {
	next, ok := NextRun(*s, now)
	if !ok {
		s.NextRunAt = nil
		return false
	}
	s.NextRunAt = &next
	return true
}
