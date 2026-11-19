package policies

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/vaultix/vaultix/internal/domain"
	policyuc "github.com/vaultix/vaultix/internal/usecase/policy"
	scheduleuc "github.com/vaultix/vaultix/internal/usecase/schedule"
)

var validate = validator.New()

// ---- Shared sub-DTOs ----

type compressionDTO struct {
	Algorithm string `json:"algorithm"`
	Level     int    `json:"level"`
}

func (c compressionDTO) toDomain() domain.Compression {
	return domain.Compression{Algorithm: c.Algorithm, Level: c.Level}
}

func compressionFromDomain(c domain.Compression) compressionDTO {
	return compressionDTO{Algorithm: c.Algorithm, Level: c.Level}
}

type encryptionDTO struct {
	Enabled bool   `json:"enabled"`
	KeyRef  string `json:"key_ref"`
}

func (e encryptionDTO) toDomain() domain.Encryption {
	return domain.Encryption{Enabled: e.Enabled, KeyRef: e.KeyRef}
}

func encryptionFromDomain(e domain.Encryption) encryptionDTO {
	return encryptionDTO{Enabled: e.Enabled, KeyRef: e.KeyRef}
}

type retentionDTO struct {
	KeepLastN   int `json:"keep_last_n"`
	KeepDays    int `json:"keep_days"`
	KeepMinimum int `json:"keep_minimum"`
}

func (r retentionDTO) toDomain() domain.RetentionRule {
	return domain.RetentionRule{KeepLastN: r.KeepLastN, KeepDays: r.KeepDays, KeepMinimum: r.KeepMinimum}
}

func retentionFromDomain(r domain.RetentionRule) retentionDTO {
	return retentionDTO{KeepLastN: r.KeepLastN, KeepDays: r.KeepDays, KeepMinimum: r.KeepMinimum}
}

// ---- Policy DTOs ----

// targetRequest is one target selection. Deep per-type rules live in the
// use-case (single source of truth); the DTO enforces shape + known types.
type targetRequest struct {
	Type          string            `json:"type" validate:"required,oneof=host_filesystem docker_config docker_volume postgres mysql mssql redis app_stack"`
	Method        string            `json:"method"`
	Selector      string            `json:"selector" validate:"required"`
	Include       []string          `json:"include"`
	Exclude       []string          `json:"exclude"`
	Options       map[string]string `json:"options"`
	CredentialRef string            `json:"credential_ref"`
}

func (t targetRequest) toInput() policyuc.TargetInput {
	return policyuc.TargetInput{
		Type:          domain.TargetType(t.Type),
		Method:        domain.BackupMethod(t.Method),
		Selector:      t.Selector,
		Include:       t.Include,
		Exclude:       t.Exclude,
		Options:       t.Options,
		CredentialRef: t.CredentialRef,
	}
}

// policyRequest is the full policy write shape (create + full-replace update).
type policyRequest struct {
	Name        string          `json:"name" validate:"required"`
	Description string          `json:"description"`
	AgentIDs    []string        `json:"agent_ids" validate:"required,min=1"`
	StorageID   string          `json:"storage_id"`
	Compression compressionDTO  `json:"compression"`
	Encryption  encryptionDTO   `json:"encryption"`
	Retention   *retentionDTO   `json:"retention" validate:"required"`
	Targets     []targetRequest `json:"targets" validate:"required,min=1,dive"`
	Note        string          `json:"note"`
}

func (r *policyRequest) check() error {
	if err := validate.Struct(r); err != nil {
		return badRequest("invalid policy: %s", compactValidationError(err))
	}
	for _, a := range r.AgentIDs {
		if strings.TrimSpace(a) == "" {
			return badRequest("agent_ids must not contain blank entries")
		}
	}
	if r.Retention.KeepLastN == 0 && r.Retention.KeepDays == 0 {
		return badRequest("retention needs at least one bound (keep_last_n or keep_days)")
	}
	if r.Retention.KeepLastN < 0 || r.Retention.KeepDays < 0 || r.Retention.KeepMinimum < 0 {
		return badRequest("retention values must be non-negative")
	}
	if r.Encryption.Enabled && strings.TrimSpace(r.Encryption.KeyRef) == "" {
		return badRequest("encryption.key_ref is required when encryption is enabled")
	}
	return nil
}

func (r *policyRequest) createInput(actor string) policyuc.CreateInput {
	return policyuc.CreateInput{
		Name: r.Name, Description: r.Description, AgentIDs: r.AgentIDs,
		StorageID: r.StorageID, Compression: r.Compression.toDomain(),
		Encryption: r.Encryption.toDomain(), Retention: r.Retention.toDomain(),
		Targets: targetInputs(r.Targets), CreatedBy: actor, Note: r.Note,
	}
}

func (r *policyRequest) updateInput(actor string) policyuc.UpdateInput {
	return policyuc.UpdateInput{
		Name: r.Name, Description: r.Description, AgentIDs: r.AgentIDs,
		StorageID: r.StorageID, Compression: r.Compression.toDomain(),
		Encryption: r.Encryption.toDomain(), Retention: r.Retention.toDomain(),
		Targets: targetInputs(r.Targets), UpdatedBy: actor, Note: r.Note,
	}
}

func targetInputs(ts []targetRequest) []policyuc.TargetInput {
	out := make([]policyuc.TargetInput, len(ts))
	for i, t := range ts {
		out[i] = t.toInput()
	}
	return out
}

// publishRequest replaces the frozen config (→ new version).
type publishRequest struct {
	Targets     []targetRequest `json:"targets" validate:"required,min=1,dive"`
	Compression compressionDTO  `json:"compression"`
	Encryption  encryptionDTO   `json:"encryption"`
	Retention   *retentionDTO   `json:"retention" validate:"required"`
	StorageID   string          `json:"storage_id"`
	Note        string          `json:"note"`
}

func (r *publishRequest) check() error {
	if err := validate.Struct(r); err != nil {
		return badRequest("invalid version: %s", compactValidationError(err))
	}
	if r.Retention.KeepLastN == 0 && r.Retention.KeepDays == 0 {
		return badRequest("retention needs at least one bound (keep_last_n or keep_days)")
	}
	if r.Encryption.Enabled && strings.TrimSpace(r.Encryption.KeyRef) == "" {
		return badRequest("encryption.key_ref is required when encryption is enabled")
	}
	return nil
}

func (r *publishRequest) toInput(actor string) policyuc.PublishInput {
	return policyuc.PublishInput{
		Targets: targetInputs(r.Targets), Compression: r.Compression.toDomain(),
		Encryption: r.Encryption.toDomain(), Retention: r.Retention.toDomain(),
		StorageID: r.StorageID, CreatedBy: actor, Note: r.Note,
	}
}

type policyDTO struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	AgentIDs       []string       `json:"agent_ids"`
	StorageID      string         `json:"storage_id"`
	Compression    compressionDTO `json:"compression"`
	Encryption     encryptionDTO  `json:"encryption"`
	Enabled        bool           `json:"enabled"`
	CurrentVersion int            `json:"current_version"`
	CreatedBy      string         `json:"created_by"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

func policyFromDomain(p *domain.BackupPolicy) policyDTO {
	return policyDTO{
		ID: p.ID, Name: p.Name, Description: p.Description, AgentIDs: p.AgentIDs,
		StorageID: p.StorageID, Compression: compressionFromDomain(p.Compression),
		Encryption: encryptionFromDomain(p.Encryption), Enabled: p.Enabled,
		CurrentVersion: p.CurrentVersion, CreatedBy: p.CreatedBy,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

type targetDTO struct {
	ID            string            `json:"id"`
	Type          string            `json:"type"`
	Method        string            `json:"method"`
	Selector      string            `json:"selector"`
	Include       []string          `json:"include"`
	Exclude       []string          `json:"exclude"`
	Options       map[string]string `json:"options"`
	CredentialRef string            `json:"credential_ref"`
}

func targetFromDomain(t domain.BackupPolicyTarget) targetDTO {
	return targetDTO{
		ID: t.ID, Type: string(t.Type), Method: string(t.Method), Selector: t.Selector,
		Include: t.Include, Exclude: t.Exclude, Options: t.Options, CredentialRef: t.CredentialRef,
	}
}

type versionDTO struct {
	Version     int            `json:"version"`
	PolicyID    string         `json:"policy_id"`
	CreatedBy   string         `json:"created_by"`
	CreatedAt   time.Time      `json:"created_at"`
	Targets     []targetDTO    `json:"targets"`
	Schedule    *scheduleDTO   `json:"schedule,omitempty"`
	Retention   retentionDTO   `json:"retention"`
	Compression compressionDTO `json:"compression"`
	Encryption  encryptionDTO  `json:"encryption"`
	StorageID   string         `json:"storage_id"`
	Note        string         `json:"note"`
}

func versionFromDomain(v *domain.BackupPolicyVersion) versionDTO {
	targets := make([]targetDTO, len(v.Targets))
	for i, t := range v.Targets {
		targets[i] = targetFromDomain(t)
	}
	out := versionDTO{
		Version: v.Version, PolicyID: v.PolicyID, CreatedBy: v.CreatedBy,
		CreatedAt: v.CreatedAt, Targets: targets,
		Retention:   retentionFromDomain(v.Retention),
		Compression: compressionFromDomain(v.Compression),
		Encryption:  encryptionFromDomain(v.Encryption),
		StorageID:   v.StorageID, Note: v.Note,
	}
	if v.Schedule.Kind != "" {
		s := scheduleFromDomain(&v.Schedule)
		out.Schedule = &s
	}
	return out
}

// ---- Schedule DTOs ----

// scheduleRequest is the schedule write shape. policy_id is required on
// create and ignored on update (schedules never move between policies).
type scheduleRequest struct {
	PolicyID       string     `json:"policy_id"`
	Kind           string     `json:"kind" validate:"required,oneof=one_time hourly daily weekly cron"`
	TimeOfDay      string     `json:"time_of_day"`
	Weekday        *int       `json:"weekday"`
	IntervalHours  int        `json:"interval_hours"`
	CronExpr       string     `json:"cron_expr"`
	RunAt          *time.Time `json:"run_at"`
	Timezone       string     `json:"timezone" validate:"required"`
	MaxConcurrency *int       `json:"max_concurrency"`
}

// checkCreate validates create semantics (policy required).
func (r *scheduleRequest) checkCreate() error {
	if strings.TrimSpace(r.PolicyID) == "" {
		return badRequest("policy_id is required")
	}
	return r.checkTiming()
}

// checkUpdate validates update semantics (policy ignored).
func (r *scheduleRequest) checkUpdate() error {
	return r.checkTiming()
}

// checkTiming enforces kind ↔ field coherence, cron semantics, and timezones.
func (r *scheduleRequest) checkTiming() error {
	if err := validate.Struct(r); err != nil {
		return badRequest("invalid schedule: %s", compactValidationError(err))
	}
	switch domain.ScheduleKind(r.Kind) {
	case domain.ScheduleOneTime:
		if r.RunAt == nil || r.RunAt.IsZero() {
			return badRequest("run_at is required for one_time schedules")
		}
	case domain.ScheduleHourly:
		if r.IntervalHours <= 0 {
			return badRequest("interval_hours must be positive for hourly schedules")
		}
	case domain.ScheduleDaily:
		if err := checkTimeOfDay(r.TimeOfDay); err != nil {
			return err
		}
	case domain.ScheduleWeekly:
		if err := checkTimeOfDay(r.TimeOfDay); err != nil {
			return err
		}
		if r.Weekday == nil || *r.Weekday < 0 || *r.Weekday > 6 {
			return badRequest("weekday must be 0..6 (0 = Sunday) for weekly schedules")
		}
	case domain.ScheduleCron:
		if _, err := scheduleuc.ParseCron(r.CronExpr); err != nil {
			return badRequest("invalid cron expression: %s", cronCause(err))
		}
	}
	if _, err := time.LoadLocation(r.Timezone); err != nil {
		return badRequest("invalid timezone %q: use an IANA name such as UTC or Asia/Jakarta", r.Timezone)
	}
	if r.MaxConcurrency != nil && *r.MaxConcurrency < 0 {
		return badRequest("max_concurrency must be >= 0 (0 = unlimited)")
	}
	return nil
}

func (r *scheduleRequest) createInput() scheduleuc.CreateInput {
	return scheduleuc.CreateInput{
		PolicyID: r.PolicyID, Kind: domain.ScheduleKind(r.Kind), TimeOfDay: r.TimeOfDay,
		Weekday: intToWeekday(r.Weekday), IntervalHours: r.IntervalHours,
		CronExpr: r.CronExpr, RunAt: r.RunAt, Timezone: r.Timezone,
		MaxConcurrency: r.MaxConcurrency,
	}
}

func (r *scheduleRequest) updateInput() scheduleuc.UpdateInput {
	return scheduleuc.UpdateInput{
		Kind: domain.ScheduleKind(r.Kind), TimeOfDay: r.TimeOfDay,
		Weekday: intToWeekday(r.Weekday), IntervalHours: r.IntervalHours,
		CronExpr: r.CronExpr, RunAt: r.RunAt, Timezone: r.Timezone,
		MaxConcurrency: r.MaxConcurrency,
	}
}

func intToWeekday(v *int) *time.Weekday {
	if v == nil {
		return nil
	}
	wd := time.Weekday(*v)
	return &wd
}

func weekdayToInt(wd *time.Weekday) *int {
	if wd == nil {
		return nil
	}
	v := int(*wd)
	return &v
}

func checkTimeOfDay(v string) error {
	if _, err := time.Parse("15:04", v); err != nil {
		return badRequest("time_of_day must be HH:MM (24h), got %q", v)
	}
	return nil
}

// cronCause strips the sentinel prefix for a readable DTO error.
func cronCause(err error) string {
	msg := err.Error()
	if i := strings.LastIndex(msg, ": "); i >= 0 {
		return msg[i+2:]
	}
	return msg
}

type scheduleDTO struct {
	ID             string     `json:"id"`
	PolicyID       string     `json:"policy_id"`
	Kind           string     `json:"kind"`
	TimeOfDay      string     `json:"time_of_day,omitempty"`
	Weekday        *int       `json:"weekday,omitempty"`
	IntervalHours  int        `json:"interval_hours,omitempty"`
	CronExpr       string     `json:"cron_expr,omitempty"`
	RunAt          *time.Time `json:"run_at,omitempty"`
	Timezone       string     `json:"timezone"`
	Paused         bool       `json:"paused"`
	MaxConcurrency int        `json:"max_concurrency"`
	NextRunAt      *time.Time `json:"next_run_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func scheduleFromDomain(s *domain.Schedule) scheduleDTO {
	return scheduleDTO{
		ID: s.ID, PolicyID: s.PolicyID, Kind: string(s.Kind), TimeOfDay: s.TimeOfDay,
		Weekday: weekdayToInt(s.Weekday), IntervalHours: s.IntervalHours,
		CronExpr: s.CronExpr, RunAt: s.RunAt, Timezone: s.Timezone,
		Paused: s.Paused, MaxConcurrency: s.MaxConcurrency, NextRunAt: s.NextRunAt,
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
}

// runRequest triggers a manual run; agent_id "" fans out to all agents.
type runRequest struct {
	AgentID string `json:"agent_id"`
}

type jobDTO struct {
	ID            string    `json:"id"`
	AgentID       string    `json:"agent_id"`
	PolicyID      string    `json:"policy_id"`
	PolicyVersion int       `json:"policy_version"`
	ScheduleID    string    `json:"schedule_id"`
	Trigger       string    `json:"trigger"`
	State         string    `json:"state"`
	CreatedBy     string    `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
}

func jobFromDomain(j *domain.BackupJob) jobDTO {
	return jobDTO{
		ID: j.ID, AgentID: j.AgentID, PolicyID: j.PolicyID,
		PolicyVersion: j.PolicyVersion, ScheduleID: j.ScheduleID,
		Trigger: string(j.Trigger), State: string(j.State),
		CreatedBy: j.CreatedBy, CreatedAt: j.CreatedAt,
	}
}

// compactValidationError renders validator failures as "field: rule" pairs.
func compactValidationError(err error) string {
	if v, ok := err.(validator.ValidationErrors); ok {
		parts := make([]string, 0, len(v))
		for _, fe := range v {
			parts = append(parts, fmt.Sprintf("%s: %s", strings.ToLower(fe.Field()), fe.Tag()))
		}
		return strings.Join(parts, "; ")
	}
	return err.Error()
}
