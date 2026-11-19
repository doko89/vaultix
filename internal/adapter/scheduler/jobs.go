package scheduler

// Job args. Each struct follows the River job-args convention — JSON tags
// plus a Kind() method — so integrator-2 registers them with a River client
// unchanged (args become river.JobArgs[T] payloads; workers below become
// river.Worker[T] implementations whose Work unwraps job.Args and calls the
// Work method defined here).

// BackupExecutionArgs triggers one schedule-driven backup dispatch. The
// Manager never runs backup payloads itself: the worker asks the jobs
// service to create the backup job row, and the agent polls and executes it
// (PRD §6.2 control/data-plane split).
type BackupExecutionArgs struct {
	AgentID    string `json:"agent_id"`
	PolicyID   string `json:"policy_id"`
	ScheduleID string `json:"schedule_id"`
}

// Kind implements the River job-args convention.
func (BackupExecutionArgs) Kind() string { return "backup-execution" }

// Queue routes backup-execution jobs.
func (BackupExecutionArgs) Queue() string { return QueueBackup }

// RetentionArgs triggers one retention-enforcement pass for a policy.
type RetentionArgs struct {
	PolicyID string `json:"policy_id"`
	// Trigger records what scheduled the pass: "schedule" | "manual".
	Trigger string `json:"trigger"`
}

// Kind implements the River job-args convention.
func (RetentionArgs) Kind() string { return "retention" }

// Queue routes retention jobs.
func (RetentionArgs) Queue() string { return QueueRetention }
