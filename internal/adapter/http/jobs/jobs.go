package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"

	"github.com/vaultix/vaultix/internal/domain"
	"github.com/vaultix/vaultix/internal/usecase/job"
)

// Service is the job use-case surface this package needs. It mirrors
// *job.Service (see compat_test.go for the conformance assertion) so
// handlers stay unit-testable behind fakes.
type Service interface {
	EnqueueManual(ctx context.Context, agentID, policyID, createdBy string) (*domain.BackupJob, error)
	Get(ctx context.Context, jobID string) (*domain.BackupJob, error)
	List(ctx context.Context, opts domain.ListOptions, f job.Filter) (domain.ListResult[domain.BackupJob], error)
	Retry(ctx context.Context, jobID, createdBy string) (*domain.BackupJob, error)
	Cancel(ctx context.Context, jobID string) (*domain.BackupJob, error)
	RetryHistory(ctx context.Context, jobID string) ([]*domain.BackupJob, error)
}

// Deps wires the routes. Validate is optional; Mount builds a default one.
// WriteGate optionally enforces roles on the mutating routes (create, retry,
// cancel); reads stay open to any caller the outer router authenticated. Nil
// disables the role check (unit tests, unwired gates).
type Deps struct {
	Service   Service
	Validate  *validator.Validate
	WriteGate func(http.HandlerFunc) http.HandlerFunc
}

// Mount registers the job routes on r.
func Mount(r chi.Router, deps Deps) {
	h := &handler{svc: deps.Service, validate: deps.Validate}
	if h.validate == nil {
		h.validate = validator.New()
	}
	write := deps.WriteGate
	if write == nil {
		write = func(next http.HandlerFunc) http.HandlerFunc { return next }
	}
	r.Route("/api/jobs", func(r chi.Router) {
		r.Get("/", h.list)
		r.Post("/", write(h.create))
		r.Get("/{id}", h.detail)
		r.Post("/{id}/retry", write(h.retry))
		r.Post("/{id}/cancel", write(h.cancel))
	})
}

type handler struct {
	svc      Service
	validate *validator.Validate
}

// ---- DTOs ----

// JobDTO is the PRD §7.10 job shape: identity, agent/policy/version,
// timestamps, state, measurable progress, byte counters, compression ratio,
// encryption status, destination, snapshot refs, error details, and retry
// linkage. Pointer fields serialize as null when not measurable.
type JobDTO struct {
	ID            string   `json:"id"`
	AgentID       string   `json:"agent_id"`
	PolicyID      string   `json:"policy_id"`
	PolicyVersion int      `json:"policy_version"`
	ScheduleID    string   `json:"schedule_id,omitempty"`
	Trigger       string   `json:"trigger"`
	State         string   `json:"state"`
	Progress      *float64 `json:"progress"`

	BytesRead        *uint64  `json:"bytes_read"`
	BytesCompressed  *uint64  `json:"bytes_compressed"`
	BytesUploaded    *uint64  `json:"bytes_uploaded"`
	CompressionRatio *float64 `json:"compression_ratio"`

	Encrypted bool   `json:"encrypted"`
	KeyRef    string `json:"key_ref,omitempty"`

	StorageID  string `json:"storage_id,omitempty"`
	SnapshotID string `json:"snapshot_id,omitempty"`

	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	RetryOf      string `json:"retry_of,omitempty"`
	Attempt      int    `json:"attempt"`

	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	CreatedBy  string     `json:"created_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func toJobDTO(j *domain.BackupJob) JobDTO {
	return JobDTO{
		ID:               j.ID,
		AgentID:          j.AgentID,
		PolicyID:         j.PolicyID,
		PolicyVersion:    j.PolicyVersion,
		ScheduleID:       j.ScheduleID,
		Trigger:          string(j.Trigger),
		State:            string(j.State),
		Progress:         j.Progress,
		BytesRead:        j.BytesRead,
		BytesCompressed:  j.BytesCompressed,
		BytesUploaded:    j.BytesUploaded,
		CompressionRatio: j.CompressionRatio(),
		Encrypted:        j.Encrypted,
		KeyRef:           j.KeyRef,
		StorageID:        j.StorageID,
		SnapshotID:       j.SnapshotID,
		ErrorCode:        j.ErrorCode,
		ErrorMessage:     j.ErrorMessage,
		RetryOf:          j.RetryOf,
		Attempt:          j.Attempt,
		StartedAt:        j.StartedAt,
		FinishedAt:       j.FinishedAt,
		CreatedBy:        j.CreatedBy,
		CreatedAt:        j.CreatedAt,
		UpdatedAt:        j.UpdatedAt,
	}
}

// AttemptDTO summarizes one entry of a job's retry history.
type AttemptDTO struct {
	ID      string `json:"id"`
	Attempt int    `json:"attempt"`
	State   string `json:"state"`
}

func toAttemptDTO(j *domain.BackupJob) AttemptDTO {
	return AttemptDTO{ID: j.ID, Attempt: j.Attempt, State: string(j.State)}
}

// DetailDTO is JobDTO plus retry history.
type DetailDTO struct {
	JobDTO
	RetryHistory []AttemptDTO `json:"retry_history"`
}

// ListDTO is one page of jobs.
type ListDTO struct {
	Items  []JobDTO `json:"items"`
	Total  int      `json:"total"`
	Limit  int      `json:"limit"`
	Offset int      `json:"offset"`
}

// CreateJobRequest enqueues a manual job.
type CreateJobRequest struct {
	AgentID  string `json:"agent_id" validate:"required"`
	PolicyID string `json:"policy_id" validate:"required"`
}

// ErrorDTO is the uniform error shape.
type ErrorDTO struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody carries a machine-readable code and a human message.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ---- Handlers ----

const (
	defaultListLimit = 50
	maxListLimit     = 200
	maxBodyBytes     = 1 << 20
)

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	opts, filter, err := parseListQuery(q)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation", err.Error())
		return
	}
	res, err := h.svc.List(r.Context(), opts, filter)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	items := make([]JobDTO, 0, len(res.Items))
	for i := range res.Items {
		items = append(items, toJobDTO(&res.Items[i]))
	}
	writeJSON(w, http.StatusOK, ListDTO{Items: items, Total: res.Total, Limit: opts.Limit, Offset: opts.Offset})
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	var req CreateJobRequest
	if err := decodeBody(w, r, h.validate, &req); err != nil {
		return // decodeBody already wrote the error
	}
	j, err := h.svc.EnqueueManual(r.Context(), req.AgentID, req.PolicyID, "")
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toJobDTO(j))
}

func (h *handler) detail(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "validation", "job id is required")
		return
	}
	j, err := h.svc.Get(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	hist, err := h.svc.RetryHistory(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	attempts := make([]AttemptDTO, 0, len(hist))
	for _, a := range hist {
		attempts = append(attempts, toAttemptDTO(a))
	}
	writeJSON(w, http.StatusOK, DetailDTO{JobDTO: toJobDTO(j), RetryHistory: attempts})
}

func (h *handler) retry(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "validation", "job id is required")
		return
	}
	j, err := h.svc.Retry(r.Context(), id, "")
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toJobDTO(j))
}

func (h *handler) cancel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "validation", "job id is required")
		return
	}
	j, err := h.svc.Cancel(r.Context(), id)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toJobDTO(j))
}

// ---- Query parsing ----

func parseListQuery(q map[string][]string) (domain.ListOptions, job.Filter, error) {
	get := func(k string) string {
		if v, ok := q[k]; ok && len(v) > 0 {
			return v[0]
		}
		return ""
	}
	opts := domain.ListOptions{Limit: defaultListLimit}
	if s := get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return opts, job.Filter{}, fmt.Errorf("invalid limit %q", s)
		}
		if n > maxListLimit {
			n = maxListLimit
		}
		opts.Limit = n
	}
	if s := get("offset"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return opts, job.Filter{}, fmt.Errorf("invalid offset %q", s)
		}
		opts.Offset = n
	}
	var f job.Filter
	f.AgentID = get("agent_id")
	f.PolicyID = get("policy_id")
	if s := get("state"); s != "" {
		st := domain.JobState(s)
		if !validState(st) {
			return opts, job.Filter{}, fmt.Errorf("invalid state %q", s)
		}
		f.State = st
	}
	if s := get("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return opts, job.Filter{}, fmt.Errorf("invalid since %q: want RFC3339", s)
		}
		f.Since = &t
	}
	if s := get("until"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return opts, job.Filter{}, fmt.Errorf("invalid until %q: want RFC3339", s)
		}
		f.Until = &t
	}
	return opts, f, nil
}

func validState(s domain.JobState) bool {
	switch s {
	case domain.JobQueued, domain.JobAssigned, domain.JobRunning, domain.JobUploading,
		domain.JobVerifying, domain.JobSucceeded, domain.JobFailed, domain.JobCancelled:
		return true
	default:
		return false
	}
}

// ---- Local helpers ----

func decodeBody(w http.ResponseWriter, r *http.Request, v *validator.Validate, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "validation", "invalid JSON body: "+err.Error())
		return err
	}
	if err := v.Struct(dst); err != nil {
		writeError(w, http.StatusBadRequest, "validation", "invalid request: "+err.Error())
		return err
	}
	return nil
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, domain.ErrValidation):
		writeError(w, http.StatusBadRequest, "validation", err.Error())
	case errors.Is(err, domain.ErrConflict):
		writeError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, domain.ErrForbidden):
		writeError(w, http.StatusForbidden, "forbidden", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, ErrorDTO{Error: ErrorBody{Code: code, Message: message}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
