package repo

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vaultix/vaultix/db/gen"
	"github.com/vaultix/vaultix/internal/domain"
)

// ScheduleRepo implements domain.ScheduleRepository on schedules.
//
// Mapping notes: the schema schedule_type allows only once/hourly/daily/
// weekly, so the remaining domain kinds and their clocks are encoded into
// cron_expression by an internal versioned codec:
//
//	v1:once:<rfc3339>    one-time RunAt
//	v1:daily:HH:MM       daily TimeOfDay
//	v1:weekly:<dow>:HH:MM weekly Weekday (0=Sunday) + TimeOfDay
//	v1:disabled           disabled kind (stored with type "once" and a NULL
//	                    next_run_at so it can never be due)
//	<5-field cron>        cron kind (stored with type "hourly" and a NULL
//	                    interval, which hourly never produces)
//
// Hourly schedules use interval_minutes and a NULL expression. Timezone,
// paused, concurrency limit, and next_run_at map to their columns; the
// schedule name column stays empty (domain has no name) and last_run_at is
// write-only for the scheduler worker.
type ScheduleRepo struct {
	baseRepo
}

// scheduleCodecVersion prefixes every encoded expression.
const scheduleCodecVersion = "v1"

// CreateSchedule inserts a schedule. The ID is database-generated.
func (r *ScheduleRepo) CreateSchedule(ctx context.Context, s *domain.Schedule) error {
	if s == nil {
		return validationError("schedule is required")
	}
	if err := s.Validate(); err != nil {
		return err
	}
	pid, err := mustParseUUID(s.PolicyID, "policy id")
	if err != nil {
		return err
	}
	codec, err := encodeSchedule(s)
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.CreateSchedule(ctx, gen.CreateScheduleParams{
		PolicyID:         pid,
		Name:             "",
		ScheduleType:     codec.scheduleType,
		CronExpression:   codec.cronExpression,
		IntervalMinutes:  codec.intervalMinutes,
		Timezone:         s.Timezone,
		NextRunAt:        codec.nextRunAt,
		ConcurrencyLimit: int32OrZero(s.MaxConcurrency),
	})
	if err != nil {
		return mapErr(err)
	}
	s.ID = uuidString(row.ID)
	if s.Kind == domain.ScheduleDisabled {
		s.NextRunAt = nil
	}
	s.CreatedAt = timeOrZero(row.CreatedAt)
	s.UpdatedAt = timeOrZero(row.UpdatedAt)
	return nil
}

// GetSchedule returns one schedule by ID.
func (r *ScheduleRepo) GetSchedule(ctx context.Context, id string) (*domain.Schedule, error) {
	sid, err := mustParseUUID(id, "schedule id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetScheduleByID(ctx, gen.GetScheduleByIDParams{ID: sid})
	if err != nil {
		return nil, mapErr(err)
	}
	s, err := mapSchedule(row)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// UpdateSchedule rewrites a schedule.
func (r *ScheduleRepo) UpdateSchedule(ctx context.Context, s *domain.Schedule) error {
	if s == nil {
		return validationError("schedule is required")
	}
	if err := s.Validate(); err != nil {
		return err
	}
	sid, err := mustParseUUID(s.ID, "schedule id")
	if err != nil {
		return err
	}
	codec, err := encodeSchedule(s)
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	_, err = r.q.UpdateSchedule(ctx, gen.UpdateScheduleParams{
		ID:               sid,
		ScheduleType:     codec.scheduleType,
		CronExpression:   codec.cronExpression,
		IntervalMinutes:  codec.intervalMinutes,
		Timezone:         s.Timezone,
		NextRunAt:        codec.nextRunAt,
		IsPaused:         s.Paused,
		ConcurrencyLimit: int32OrZero(s.MaxConcurrency),
	})
	if err != nil {
		return mapErr(err)
	}
	if s.Kind == domain.ScheduleDisabled {
		s.NextRunAt = nil
	}
	return nil
}

// DeleteSchedule removes a schedule after verifying it exists.
func (r *ScheduleRepo) DeleteSchedule(ctx context.Context, id string) error {
	sid, err := mustParseUUID(id, "schedule id")
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	if _, err := r.q.GetScheduleByID(ctx, gen.GetScheduleByIDParams{ID: sid}); err != nil {
		return mapErr(err)
	}
	if err := r.q.DeleteSchedule(ctx, gen.DeleteScheduleParams{ID: sid}); err != nil {
		return mapErr(err)
	}
	return nil
}

// ListSchedules returns all schedules of a policy.
func (r *ScheduleRepo) ListSchedules(ctx context.Context, policyID string) ([]domain.Schedule, error) {
	pid, err := mustParseUUID(policyID, "policy id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	rows, err := r.q.ListSchedulesByPolicy(ctx, gen.ListSchedulesByPolicyParams{PolicyID: pid})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]domain.Schedule, 0, len(rows))
	for _, row := range rows {
		s, err := mapSchedule(row)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// DueSchedules returns active schedules with NextRunAt <= now, oldest
// first. The scheduler worker claims each row with RecordScheduleRun;
// concurrent workers must coordinate externally (single worker assumed).
func (r *ScheduleRepo) DueSchedules(ctx context.Context, now time.Time, limit int) ([]domain.Schedule, error) {
	if limit <= 0 {
		limit = defaultPageLimit
	}
	if limit > maxPageLimit {
		limit = maxPageLimit
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	rows, err := r.q.ListDueSchedulesBefore(ctx, gen.ListDueSchedulesBeforeParams{
		NextRunAt: timestamptz(now),
		Limit:     int32OrZero(limit),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]domain.Schedule, 0, len(rows))
	for _, row := range rows {
		s, err := mapSchedule(row)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// scheduleCodec is the encoded schedule_type/cron_expression/interval
// triple plus the effective next run.
type scheduleCodec struct {
	scheduleType    string
	cronExpression  pgtype.Text
	intervalMinutes pgtype.Int4
	nextRunAt       pgtype.Timestamptz
}

func encodeSchedule(s *domain.Schedule) (scheduleCodec, error) {
	out := scheduleCodec{nextRunAt: timestamptzPtr(s.NextRunAt)}
	switch s.Kind {
	case domain.ScheduleOneTime:
		if s.RunAt == nil {
			return out, validationError("one-time schedule requires run_at")
		}
		out.scheduleType = "once"
		out.cronExpression = textOrNull(scheduleCodecVersion + ":once:" + s.RunAt.UTC().Format(time.RFC3339))
	case domain.ScheduleHourly:
		if s.IntervalHours <= 0 {
			return out, validationError("hourly schedule requires a positive interval")
		}
		out.scheduleType = "hourly"
		out.intervalMinutes = int4OrNull(s.IntervalHours * 60)
	case domain.ScheduleDaily:
		out.scheduleType = "daily"
		out.cronExpression = textOrNull(scheduleCodecVersion + ":daily:" + s.TimeOfDay)
	case domain.ScheduleWeekly:
		if s.Weekday == nil {
			return out, validationError("weekly schedule requires a weekday")
		}
		out.scheduleType = "weekly"
		out.cronExpression = textOrNull(scheduleCodecVersion + ":weekly:" + strconv.Itoa(int(*s.Weekday)) + ":" + s.TimeOfDay)
	case domain.ScheduleCron:
		out.scheduleType = "hourly" // marker pairing: hourly + NULL interval + raw cron
		out.cronExpression = textOrNull(s.CronExpr)
	case domain.ScheduleDisabled:
		out.scheduleType = "once"
		out.cronExpression = textOrNull(scheduleCodecVersion + ":disabled")
		out.nextRunAt = pgtype.Timestamptz{} // never due
	default:
		return out, validationError(fmt.Sprintf("unknown schedule kind %q", s.Kind))
	}
	return out, nil
}

func mapSchedule(row gen.Schedule) (domain.Schedule, error) {
	s := domain.Schedule{
		ID:             uuidString(row.ID),
		PolicyID:       uuidString(row.PolicyID),
		Timezone:       row.Timezone,
		Paused:         row.IsPaused,
		MaxConcurrency: int(row.ConcurrencyLimit),
		NextRunAt:      timePtr(row.NextRunAt),
		CreatedAt:      timeOrZero(row.CreatedAt),
		UpdatedAt:      timeOrZero(row.UpdatedAt),
	}
	expr := textOrEmpty(row.CronExpression)
	if rest, ok := strings.CutPrefix(expr, scheduleCodecVersion+":"); ok {
		return decodeVersionedSchedule(s, rest)
	}
	switch row.ScheduleType {
	case "once":
		s.Kind = domain.ScheduleOneTime
		s.RunAt = timePtr(row.NextRunAt)
	case "hourly":
		if !row.IntervalMinutes.Valid && expr != "" {
			// Marker pairing written by encodeSchedule for cron kinds.
			s.Kind = domain.ScheduleCron
			s.CronExpr = expr
			break
		}
		s.Kind = domain.ScheduleHourly
		s.IntervalHours = intOrZero(row.IntervalMinutes) / 60
		if s.IntervalHours < 1 {
			s.IntervalHours = 1
		}
	case "daily":
		s.Kind = domain.ScheduleDaily
	case "weekly":
		s.Kind = domain.ScheduleWeekly
	default:
		return domain.Schedule{}, validationError("unknown stored schedule type")
	}
	return s, nil
}

func decodeVersionedSchedule(s domain.Schedule, rest string) (domain.Schedule, error) {
	kind, payload, _ := strings.Cut(rest, ":")
	switch kind {
	case "disabled":
		s.Kind = domain.ScheduleDisabled
		s.NextRunAt = nil
		return s, nil
	case "once":
		runAt, err := time.Parse(time.RFC3339, payload)
		if err != nil {
			return domain.Schedule{}, validationError("stored one-time run_at is corrupt")
		}
		s.Kind = domain.ScheduleOneTime
		s.RunAt = &runAt
		return s, nil
	case "daily":
		s.Kind = domain.ScheduleDaily
		s.TimeOfDay = payload
		return s, nil
	case "weekly":
		dow, tod, ok := strings.Cut(payload, ":")
		if !ok {
			return domain.Schedule{}, validationError("stored weekly schedule is corrupt")
		}
		n, err := strconv.Atoi(dow)
		if err != nil || n < 0 || n > 6 {
			return domain.Schedule{}, validationError("stored weekly weekday is corrupt")
		}
		weekday := time.Weekday(n)
		s.Kind = domain.ScheduleWeekly
		s.Weekday = &weekday
		s.TimeOfDay = tod
		return s, nil
	default:
		return domain.Schedule{}, validationError("unknown stored schedule encoding")
	}
}
