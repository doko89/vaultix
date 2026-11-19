package domain

import (
	"fmt"
	"strings"
	"time"
)

// ScheduleKind selects one-time vs recurring execution (PRD §7.9).
type ScheduleKind string

const (
	ScheduleOneTime  ScheduleKind = "one_time"
	ScheduleHourly   ScheduleKind = "hourly"
	ScheduleDaily    ScheduleKind = "daily"
	ScheduleWeekly   ScheduleKind = "weekly"
	ScheduleCron     ScheduleKind = "cron" // advanced escape hatch
	ScheduleDisabled ScheduleKind = "disabled"
)

// Schedule defines when a policy executes (PRD `schedules`).
// MVP recurring kinds: hourly intervals, daily, weekly.
type Schedule struct {
	ID       string
	PolicyID string
	Kind     ScheduleKind
	// TimeOfDay is "HH:MM" for daily/weekly; Weekday 0=Sunday for weekly.
	TimeOfDay string
	Weekday   *time.Weekday
	// IntervalHours for hourly schedules (every N hours).
	IntervalHours int
	// CronExpr for ScheduleCron (5-field standard cron).
	CronExpr string
	// RunAt pins the execution instant for one-time schedules.
	RunAt *time.Time
	// Timezone is an IANA name (e.g. "UTC", "Asia/Jakarta").
	Timezone string
	// Paused suspends triggering without deleting the schedule.
	Paused bool
	// MaxConcurrency caps overlapping jobs for the protected resource;
	// 1 prevents unintended overlap.
	MaxConcurrency int
	NextRunAt      *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Validate checks schedule invariants.
func (s Schedule) Validate() error {
	if strings.TrimSpace(s.PolicyID) == "" {
		return fmt.Errorf("%w: policy_id is required", ErrValidation)
	}
	switch s.Kind {
	case ScheduleOneTime:
		if s.RunAt == nil {
			return fmt.Errorf("%w: one-time schedule requires run_at", ErrValidation)
		}
	case ScheduleHourly:
		if s.IntervalHours <= 0 {
			return fmt.Errorf("%w: hourly schedule requires a positive interval", ErrValidation)
		}
	case ScheduleDaily:
		if _, err := parseTimeOfDay(s.TimeOfDay); err != nil {
			return fmt.Errorf("%w: daily schedule: %v", ErrValidation, err)
		}
	case ScheduleWeekly:
		if _, err := parseTimeOfDay(s.TimeOfDay); err != nil {
			return fmt.Errorf("%w: weekly schedule: %v", ErrValidation, err)
		}
		if s.Weekday == nil {
			return fmt.Errorf("%w: weekly schedule requires a weekday", ErrValidation)
		}
	case ScheduleCron:
		if len(strings.Fields(s.CronExpr)) != 5 {
			return fmt.Errorf("%w: cron schedule requires a 5-field expression", ErrValidation)
		}
	case ScheduleDisabled:
		// No further constraints.
	default:
		return fmt.Errorf("%w: unknown schedule kind %q", ErrValidation, s.Kind)
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return fmt.Errorf("%w: invalid timezone %q", ErrValidation, s.Timezone)
	}
	if s.MaxConcurrency < 0 {
		return fmt.Errorf("%w: max_concurrency must be non-negative", ErrValidation)
	}
	return nil
}

// parseTimeOfDay validates "HH:MM" in 24h format.
func parseTimeOfDay(v string) (time.Time, error) {
	t, err := time.Parse("15:04", v)
	if err != nil {
		return time.Time{}, fmt.Errorf("time_of_day must be HH:MM, got %q", v)
	}
	return t, nil
}

// Pause suspends triggering; Resume re-enables it.
func (s *Schedule) Pause()  { s.Paused = true }
func (s *Schedule) Resume() { s.Paused = false }

// Active reports whether the schedule may trigger new jobs.
func (s Schedule) Active() bool {
	return !s.Paused && s.Kind != ScheduleDisabled
}
