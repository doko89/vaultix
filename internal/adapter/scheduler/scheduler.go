package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Config sizes the scheduler.
type Config struct {
	// Queues overrides DefaultQueues() per name; entries may be partial
	// (non-positive fields fall back to defaults).
	Queues map[string]QueueConfig
	// Retry overrides RetryPolicyFor per job kind (see jobs.go Kind).
	// Test-only in practice: production uses the mapped policies.
	Retry map[string]RetryPolicy
	// ShutdownTimeout bounds graceful drain on Stop.
	ShutdownTimeout time.Duration
	// Logger defaults to slog.Default().
	Logger *slog.Logger
}

// Scheduler is the in-process job runner: per-queue worker pools executing
// the workers in workers.go with the retry/backoff policies in policy.go.
//
// INTEGRATION NOTE (river): this runner is intentionally ephemeral — jobs
// live in memory, so a manager restart drops queued (not running-against-
// the-DB) work. Integrator-2 replaces it with a river.Client adapter behind
// this same Scheduler API (EnqueueBackup/EnqueueRetention/Start/Stop/Health
// keep their signatures; only the constructor changes). Queue names, job
// kinds, args shapes, and retry policies carry over unchanged.
type Scheduler struct {
	queues   map[string]chan envelope
	wg       sync.WaitGroup
	mu       sync.Mutex
	running  bool
	stopped  bool
	stopCh   chan struct{}
	stopOnce sync.Once

	backup    BackupExecutionWorker
	retention RetentionWorker
	retry     map[string]RetryPolicy
	shutdown  time.Duration
	log       *slog.Logger
}

// envelope is one queued or in-flight job.
type envelope struct {
	kind     string
	backup   BackupExecutionArgs
	retain   RetentionArgs
	attempts int
}

func (s *Scheduler) policyFor(kind string) RetryPolicy {
	if p, ok := s.retry[kind]; ok {
		return p
	}
	return RetryPolicyFor(kind)
}

// New builds a Scheduler. Both executors are required: an unwired scheduler
// would silently drop every job, so serve.go skips construction (with a
// TODO(integrator-2) note) until the jobs and recovery tracks land.
func New(cfg Config, backup BackupExecutor, retention RetentionEnforcer) (*Scheduler, error) {
	if backup == nil {
		return nil, fmt.Errorf("scheduler: BackupExecutor is required")
	}
	if retention == nil {
		return nil, fmt.Errorf("scheduler: RetentionEnforcer is required")
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	defs := DefaultQueues()
	queues := make(map[string]chan envelope, len(defs))
	for name, def := range defs {
		if override, ok := cfg.Queues[name]; ok {
			if override.MaxWorkers > 0 {
				def.MaxWorkers = override.MaxWorkers
			}
			if override.MaxQueued > 0 {
				def.MaxQueued = override.MaxQueued
			}
		}
		queues[name] = make(chan envelope, def.MaxQueued)
	}
	shutdown := cfg.ShutdownTimeout
	if shutdown <= 0 {
		shutdown = 30 * time.Second
	}
	return &Scheduler{
		queues:    queues,
		stopCh:    make(chan struct{}),
		backup:    BackupExecutionWorker{Executor: backup, Log: log},
		retention: RetentionWorker{Enforcer: retention, Log: log},
		retry:     cfg.Retry,
		shutdown:  shutdown,
		log:       log,
	}, nil
}

// Start launches the worker pools and returns. Calling Start twice, or
// restarting after Stop, is an error; the context is used only to derive
// shutdown (cancel it, or call Stop, for graceful termination).
func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("scheduler: already started")
	}
	if s.stopped {
		s.mu.Unlock()
		return fmt.Errorf("scheduler: already stopped; build a new Scheduler to restart")
	}
	s.running = true
	s.mu.Unlock()

	defs := DefaultQueues()
	for name, ch := range s.queues {
		workers := defs[name].MaxWorkers
		for i := 0; i < workers; i++ {
			s.wg.Add(1)
			go s.loop(ctx, name, ch)
		}
	}
	s.log.Info("scheduler started", "queues", len(s.queues))
	return nil
}

// Stop signals shutdown and waits for in-flight jobs to finish, up to the
// configured ShutdownTimeout. Queued-but-unstarted jobs are dropped (and
// counted in the log); under River they would persist instead.
func (s *Scheduler) Stop(ctx context.Context) error {
	s.stopOnce.Do(func() { close(s.stopCh) })
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()

	timeout := s.shutdown
	if deadline, ok := ctx.Deadline(); ok {
		if d := time.Until(deadline); d < timeout {
			timeout = d
		}
	}
	select {
	case <-done:
	case <-time.After(timeout):
		return fmt.Errorf("scheduler: shutdown timed out with jobs in flight")
	case <-ctx.Done():
		return fmt.Errorf("scheduler: shutdown interrupted: %w", ctx.Err())
	}
	dropped := 0
	for _, ch := range s.queues {
		dropped += len(ch)
	}
	s.mu.Lock()
	s.running = false
	s.stopped = true
	s.mu.Unlock()
	s.log.Info("scheduler stopped", "dropped_queued", dropped)
	return nil
}

// Health reports whether the scheduler is running. Serve wires it into
// /healthz as the queue check.
func (s *Scheduler) Health(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return fmt.Errorf("scheduler: not running")
	}
	return nil
}

// EnqueueBackup queues one backup-execution job.
func (s *Scheduler) EnqueueBackup(ctx context.Context, args BackupExecutionArgs) error {
	return s.enqueue(ctx, QueueBackup, envelope{kind: BackupExecutionArgs{}.Kind(), backup: args})
}

// EnqueueRetention queues one retention job.
func (s *Scheduler) EnqueueRetention(ctx context.Context, args RetentionArgs) error {
	return s.enqueue(ctx, QueueRetention, envelope{kind: RetentionArgs{}.Kind(), retain: args})
}

func (s *Scheduler) enqueue(ctx context.Context, queue string, e envelope) error {
	s.mu.Lock()
	running := s.running
	s.mu.Unlock()
	if !running {
		return fmt.Errorf("scheduler: not running")
	}
	ch, ok := s.queues[queue]
	if !ok {
		return fmt.Errorf("scheduler: unknown queue %q", queue)
	}
	select {
	case ch <- e:
		return nil
	case <-s.stopCh:
		return fmt.Errorf("scheduler: shutting down")
	case <-ctx.Done():
		return fmt.Errorf("scheduler: enqueue %s: %w", queue, ctx.Err())
	}
}

func (s *Scheduler) loop(ctx context.Context, queue string, ch <-chan envelope) {
	defer s.wg.Done()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ctx.Done():
			return
		case e := <-ch:
			s.execute(ctx, queue, e)
		}
	}
}

func (s *Scheduler) execute(ctx context.Context, queue string, e envelope) {
	policy := s.policyFor(e.kind)
	var err error
	switch e.kind {
	case BackupExecutionArgs{}.Kind():
		err = s.backup.Work(ctx, e.backup)
	case RetentionArgs{}.Kind():
		err = s.retention.Work(ctx, e.retain)
	default:
		s.log.Error("scheduler: unknown job kind; dropping", "kind", e.kind, "queue", queue)
		return
	}
	if err == nil {
		return
	}
	e.attempts++
	if e.attempts >= policy.MaxAttempts {
		s.log.Error("scheduler: job exhausted retries; dropping (river: dead-letter)",
			"kind", e.kind, "queue", queue, "attempts", e.attempts, "error", err)
		return
	}
	delay := BackoffForAttempt(policy, e.attempts)
	s.log.Warn("scheduler: job failed; retrying",
		"kind", e.kind, "queue", queue, "attempt", e.attempts, "backoff", delay.String(), "error", err)
	// Requeue after the backoff without blocking the worker loop.
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-s.stopCh:
			return
		case <-ctx.Done():
			return
		case <-timer.C:
			ch := s.queues[queue]
			select {
			case ch <- e:
			case <-s.stopCh:
			case <-ctx.Done():
			}
		}
	}()
}
