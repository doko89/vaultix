package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// NextRun returns the next firing instant strictly after `after`, evaluated
// in the schedule's stored timezone. It returns nil when there is no future
// firing (one-time run already passed, disabled schedule, or unparseable
// timezone/cron — Validate rejects those at write time, so nil here means
// "no upcoming run", never a validation report).
//
// Daylight-saving behavior: daily/weekly fires are wall-clock anchored
// (02:30 means 02:30 local). In a spring-forward gap the Go resolver maps
// the nonexistent wall time to a nearby valid instant on the same date, so
// the job still fires that day (observed: 02:30 in the gap resolves to
// 01:30 EST — slightly early, never skipped or duplicated); in a fall-back
// overlap the first occurrence wins. Hourly fires sit on an absolute grid
// anchored at local midnight, so they never double-fire across a fallback
// (wall time may drift by the DST offset on the transition day).
func NextRun(s domain.Schedule, after time.Time) *time.Time {
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return nil
	}
	switch s.Kind {
	case domain.ScheduleOneTime:
		if s.RunAt == nil || !s.RunAt.After(after) {
			return nil
		}
		cp := *s.RunAt
		return &cp
	case domain.ScheduleHourly:
		return nextHourly(s.IntervalHours, after, loc)
	case domain.ScheduleDaily:
		return nextDaily(s.TimeOfDay, after, loc)
	case domain.ScheduleWeekly:
		if s.Weekday == nil {
			return nil
		}
		return nextWeekly(s.TimeOfDay, *s.Weekday, after, loc)
	case domain.ScheduleCron:
		spec, err := ParseCron(s.CronExpr)
		if err != nil {
			return nil
		}
		return NextCronAfter(spec, after, loc)
	default: // disabled, unknown
		return nil
	}
}

// nextHourly aligns to an absolute N-hour grid anchored at local midnight.
func nextHourly(interval int, after time.Time, loc *time.Location) *time.Time {
	if interval <= 0 {
		return nil
	}
	local := after.In(loc)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	step := time.Duration(interval) * time.Hour
	elapsed := after.Sub(midnight) // >= 0: midnight derives from after's own date
	k := elapsed/step + 1
	next := midnight.Add(k * step)
	return &next
}

// nextDaily finds the next HH:MM wall-clock occurrence strictly after `after`.
func nextDaily(hhmm string, after time.Time, loc *time.Location) *time.Time {
	h, m, err := splitTimeOfDay(hhmm)
	if err != nil {
		return nil
	}
	local := after.In(loc)
	cand := time.Date(local.Year(), local.Month(), local.Day(), h, m, 0, 0, loc)
	if !cand.After(after) {
		cand = time.Date(local.Year(), local.Month(), local.Day()+1, h, m, 0, 0, loc)
	}
	return &cand
}

// nextWeekly finds the next weekday@HH:MM occurrence strictly after `after`.
func nextWeekly(hhmm string, wd time.Weekday, after time.Time, loc *time.Location) *time.Time {
	h, m, err := splitTimeOfDay(hhmm)
	if err != nil {
		return nil
	}
	local := after.In(loc)
	delta := (int(wd) - int(local.Weekday()) + 7) % 7
	cand := time.Date(local.Year(), local.Month(), local.Day()+delta, h, m, 0, 0, loc)
	if !cand.After(after) {
		cand = cand.AddDate(0, 0, 7)
	}
	return &cand
}

func splitTimeOfDay(hhmm string) (int, int, error) {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		return 0, 0, err
	}
	return t.Hour(), t.Minute(), nil
}

// ---- Cron (5-field standard: minute hour dom month dow) ----

// CronSpec is a parsed 5-field cron expression.
type CronSpec struct {
	Minutes  map[int]bool
	Hours    map[int]bool
	Days     map[int]bool // day of month 1..31
	Months   map[int]bool // 1..12
	Weekdays map[int]bool // 0..6, Sunday = 0 (7 accepted as Sunday)

	domOpen bool // day-of-month field was "*"
	dowOpen bool // day-of-week field was "*"
}

// ParseCron parses a 5-field cron expression. Fields support "*", "*/n",
// "a-b", "a-b/n", comma lists, and month/weekday names (jan..dec, sun..sat).
// All failures wrap domain.ErrValidation.
func ParseCron(expr string) (CronSpec, error) {
	var zero CronSpec
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return zero, fmt.Errorf("%w: cron expression needs 5 fields, got %d", domain.ErrValidation, len(fields))
	}
	var err error
	spec := CronSpec{}
	if spec.Minutes, err = parseCronField(fields[0], 0, 59, nil); err != nil {
		return zero, fmt.Errorf("%w: cron minute: %v", domain.ErrValidation, err)
	}
	if spec.Hours, err = parseCronField(fields[1], 0, 23, nil); err != nil {
		return zero, fmt.Errorf("%w: cron hour: %v", domain.ErrValidation, err)
	}
	if spec.Days, err = parseCronField(fields[2], 1, 31, nil); err != nil {
		return zero, fmt.Errorf("%w: cron day-of-month: %v", domain.ErrValidation, err)
	}
	if spec.Months, err = parseCronField(fields[3], 1, 12, monthNames); err != nil {
		return zero, fmt.Errorf("%w: cron month: %v", domain.ErrValidation, err)
	}
	dow, err := parseCronField(fields[4], 0, 7, weekdayNames)
	if err != nil {
		return zero, fmt.Errorf("%w: cron day-of-week: %v", domain.ErrValidation, err)
	}
	spec.Weekdays = dow
	if dow[7] { // 7 is Sunday too
		spec.Weekdays = map[int]bool{0: true}
		for k, v := range dow {
			if k != 7 {
				spec.Weekdays[k] = v
			}
		}
	}
	spec.domOpen = fields[2] == "*"
	spec.dowOpen = fields[4] == "*"
	return spec, nil
}

var monthNames = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

var weekdayNames = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
}

// parseCronField parses one cron field into an allowed-value set.
func parseCronField(field string, lo, hi int, names map[string]int) (map[int]bool, error) {
	resolve := func(tok string) (int, error) {
		if names != nil {
			if v, ok := names[strings.ToLower(tok)]; ok {
				return v, nil
			}
		}
		v, err := strconv.Atoi(tok)
		if err != nil {
			return 0, fmt.Errorf("bad value %q", tok)
		}
		return v, nil
	}
	out := map[int]bool{}
	addRange := func(a, b, step int) error {
		if step <= 0 {
			return fmt.Errorf("step must be positive")
		}
		if a < lo || b > hi || a > b {
			return fmt.Errorf("range %d-%d outside %d-%d", a, b, lo, hi)
		}
		for v := a; v <= b; v += step {
			out[v] = true
		}
		return nil
	}
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("empty element in %q", field)
		}
		base, stepStr, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepStr)
			if err != nil || n <= 0 {
				return nil, fmt.Errorf("bad step in %q", part)
			}
			step = n
		}
		switch {
		case base == "*":
			if err := addRange(lo, hi, step); err != nil {
				return nil, err
			}
		case strings.Contains(base, "-"):
			bounds := strings.SplitN(base, "-", 2)
			a, err1 := resolve(strings.TrimSpace(bounds[0]))
			b, err2 := resolve(strings.TrimSpace(bounds[1]))
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("bad range %q", part)
			}
			if err := addRange(a, b, step); err != nil {
				return nil, err
			}
		default:
			v, err := resolve(base)
			if err != nil {
				return nil, err
			}
			if hasStep {
				if err := addRange(v, hi, step); err != nil {
					return nil, err
				}
			} else {
				if v < lo || v > hi {
					return nil, fmt.Errorf("value %d outside %d-%d", v, lo, hi)
				}
				out[v] = true
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("field %q matches nothing", field)
	}
	return out, nil
}

// Matches reports whether t (evaluated in its own location) matches the spec.
// Day-of-month and day-of-week combine with OR when both are restricted
// (Vixie cron semantics); otherwise the restricted one governs.
func (c CronSpec) Matches(t time.Time) bool {
	if !c.Minutes[t.Minute()] || !c.Hours[t.Hour()] || !c.Months[int(t.Month())] {
		return false
	}
	domMatch, dowMatch := c.Days[t.Day()], c.Weekdays[int(t.Weekday())]
	switch {
	case c.domOpen && c.dowOpen:
		return true
	case c.domOpen:
		return dowMatch
	case c.dowOpen:
		return domMatch
	default:
		return domMatch || dowMatch
	}
}

// cronScanCap bounds the forward scan (366 days of minutes).
const cronScanCap = 366 * 24 * 60

// NextCronAfter returns the first minute strictly after `after` matching the
// spec in loc, or nil when nothing matches within a year (e.g. Feb 30).
func NextCronAfter(spec CronSpec, after time.Time, loc *time.Location) *time.Time {
	cand := after.In(loc).Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < cronScanCap; i++ {
		if spec.Matches(cand) {
			out := cand
			return &out
		}
		cand = cand.Add(time.Minute)
	}
	return nil
}
