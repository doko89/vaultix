package scheduler

// Queue names. They map 1:1 onto River queue names when integrator-2 adds
// github.com/riverqueue/river (river.Config.Queues); the in-process runner
// below uses the same names so queue references in code and config are
// stable across the swap. Backed by Postgres; no Redis anywhere.
const (
	// QueueDefault carries ad-hoc and maintenance jobs.
	QueueDefault = "default"
	// QueueBackup carries backup-execution jobs (schedule-triggered and
	// manual dispatch).
	QueueBackup = "backup"
	// QueueRetention carries retention-enforcement jobs.
	QueueRetention = "retention"
	// QueueMaintenance carries notification drains and schedule bookkeeping.
	QueueMaintenance = "maintenance"
)

// QueueConfig sizes one queue's worker pool.
type QueueConfig struct {
	// MaxWorkers caps concurrent workers draining the queue.
	MaxWorkers int
	// MaxQueued caps buffered jobs waiting for a worker; enqueue blocks
	// (context-aware) once the buffer is full.
	MaxQueued int
}

// DefaultQueues returns the production queue sizing. Backup execution gets
// the widest pool; retention stays narrow because it deletes data.
func DefaultQueues() map[string]QueueConfig {
	return map[string]QueueConfig{
		QueueDefault:     {MaxWorkers: 2, MaxQueued: 100},
		QueueBackup:      {MaxWorkers: 8, MaxQueued: 1000},
		QueueRetention:   {MaxWorkers: 2, MaxQueued: 100},
		QueueMaintenance: {MaxWorkers: 1, MaxQueued: 100},
	}
}
