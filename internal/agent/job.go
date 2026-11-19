package agent

import (
	"fmt"

	"github.com/go-playground/validator/v10"
)

// JobKind enumerates the backup/restore operations the agent can execute.
type JobKind string

const (
	JobKindFilesystem  JobKind = "filesystem"
	JobKindDockerVol   JobKind = "docker-volume"
	JobKindPostgres    JobKind = "postgres"
	JobKindMySQL       JobKind = "mysql"
	JobKindRestoreFile JobKind = "restore-file"
)

// Job is a unit of work assigned by the Manager. Every job is validated at
// the boundary (ValidateJob) before any execution is attempted.
type Job struct {
	ID            string   `json:"id" validate:"required"`
	AgentID       string   `json:"agent_id" validate:"required"`
	Policy        string   `json:"policy" validate:"required"`
	PolicyVersion int      `json:"policy_version" validate:"gte=1"`
	Kind          JobKind  `json:"kind" validate:"required,oneof=filesystem docker-volume postgres mysql restore-file"`
	Targets       []string `json:"targets" validate:"required,min=1,dive,required"`
}

var jobValidator = validator.New()

// ValidationError reports a malformed job rejected at the boundary.
type ValidationError struct {
	JobID  string
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("agent: job %q rejected: malformed: %s", e.JobID, e.Reason)
}

// UnauthorizedError reports a job addressed to a different agent.
type UnauthorizedError struct {
	JobID    string
	Expected string
	Got      string
}

func (e *UnauthorizedError) Error() string {
	return fmt.Sprintf("agent: job %q rejected: unauthorized for this agent", e.JobID)
}

// ValidateJob enforces the validation boundary: structural validation first,
// then authorization (the job must be addressed to this agent).
func ValidateJob(job Job, expectedAgentID string) error {
	if err := jobValidator.Struct(job); err != nil {
		return &ValidationError{JobID: job.ID, Reason: err.Error()}
	}
	if job.AgentID != expectedAgentID {
		return &UnauthorizedError{JobID: job.ID, Expected: expectedAgentID, Got: job.AgentID}
	}
	return nil
}
