package domain

import (
	"fmt"
	"time"
)

// JobState is the lifecycle state of backup and restore jobs (PRD §7.10).
type JobState string

const (
	JobQueued    JobState = "queued"
	JobAssigned  JobState = "assigned"
	JobRunning   JobState = "running"
	JobUploading JobState = "uploading"
	JobVerifying JobState = "verifying"
	JobSucceeded JobState = "succeeded"
	JobFailed    JobState = "failed"
	JobCancelled JobState = "cancelled"
)

// Terminal reports whether the state ends the job lifecycle.
func (s JobState) Terminal() bool {
	switch s {
	case JobSucceeded, JobFailed, JobCancelled:
		return true
	default:
		return false
	}
}

// validJobTransitions is the allowed state machine (PRD §7.10 + §10.1:
// transitions are persisted; retry never fabricates a snapshot).
var validJobTransitions = map[JobState][]JobState{
	JobQueued:    {JobAssigned, JobCancelled},
	JobAssigned:  {JobRunning, JobCancelled, JobQueued}, // requeue on agent loss
	JobRunning:   {JobUploading, JobFailed, JobCancelled},
	JobUploading: {JobVerifying, JobFailed, JobCancelled},
	JobVerifying: {JobSucceeded, JobFailed, JobCancelled},
	JobFailed:    {JobQueued}, // explicit retry requeues
	// JobSucceeded and JobCancelled are terminal.
}

// CanTransitionTo reports whether moving from s to next is legal.
func (s JobState) CanTransitionTo(next JobState) bool {
	for _, n := range validJobTransitions[s] {
		if n == next {
			return true
		}
	}
	return false
}

// TransitionTo moves a job holder to next or returns ErrConflict.
func TransitionTo(from *JobState, next JobState) error {
	if !from.CanTransitionTo(next) {
		return fmt.Errorf("%w: illegal job transition %s -> %s", ErrConflict, *from, next)
	}
	*from = next
	return nil
}

// JobTrigger records how a job was started.
type JobTrigger string

const (
	JobTriggerManual   JobTrigger = "manual"
	JobTriggerSchedule JobTrigger = "schedule"
	JobTriggerRetry    JobTrigger = "retry"
)

// BackupJob is one backup execution (PRD §7.10).
type BackupJob struct {
	ID            string
	AgentID       string
	PolicyID      string
	PolicyVersion int
	// ScheduleID is empty for manual jobs; the schedule definition used is
	// frozen on the policy version.
	ScheduleID string
	Trigger    JobTrigger

	State    JobState
	Progress *float64 // 0..1 where measurable

	BytesRead       *uint64
	BytesCompressed *uint64
	BytesUploaded   *uint64

	Encrypted bool
	KeyRef    string // set when Encrypted

	StorageID  string
	SnapshotID string // set once artifacts are registered

	ErrorCode    string
	ErrorMessage string
	RetryOf      string // parent job ID when this is a retry
	Attempt      int

	StartedAt  *time.Time
	FinishedAt *time.Time
	CreatedBy  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// CompressionRatio returns compressed/read where measurable, else nil.
func (j BackupJob) CompressionRatio() *float64 {
	if j.BytesRead == nil || j.BytesCompressed == nil || *j.BytesRead == 0 {
		return nil
	}
	r := float64(*j.BytesCompressed) / float64(*j.BytesRead)
	return &r
}

// RestoreJob is one restore execution (PRD §7.13). Restores require explicit
// authorization and confirmation; overwrites warn before proceeding.
type RestoreJob struct {
	ID         string
	AgentID    string
	SnapshotID string
	// TargetPath overrides the original location when set (alternate
	// destination restore); empty restores in place with overwrite warning.
	TargetPath string
	// Paths limits the restore to a subset of the snapshot (file-level
	// restore); empty restores the whole snapshot scope.
	Paths []string

	State    JobState
	Progress *float64

	ErrorCode    string
	ErrorMessage string

	ApprovedBy string // authorizing actor for the explicit confirmation
	StartedAt  *time.Time
	FinishedAt *time.Time
	CreatedBy  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
