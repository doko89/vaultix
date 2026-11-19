package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Runner owns the agent's work loops: backup polling + execution and restore
// polling + execution. MVP runs one job at a time; a restore waits for a
// backup to finish and vice versa.
type Runner struct {
	Client   ManagerClient
	Config   Config
	Store    *Store
	Executor *Executor
	Uploader *Uploader
	Restorer *Restorer
	Logger   *slog.Logger

	mu         sync.Mutex
	runningJob string
	cancelRun  context.CancelFunc
	runningR   string
	cancelR    context.CancelFunc
	workMu     sync.Mutex // serializes backup vs restore execution
}

func (r *Runner) logger() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}

// Validate fails fast on programmer error.
func (r *Runner) Validate() error {
	if r.Client == nil {
		return fmt.Errorf("agent: runner requires a ManagerClient")
	}
	if r.Store == nil {
		return fmt.Errorf("agent: runner requires a Store")
	}
	if r.Executor == nil {
		return fmt.Errorf("agent: runner requires an Executor")
	}
	if r.Uploader == nil {
		return fmt.Errorf("agent: runner requires an Uploader")
	}
	if r.Restorer == nil {
		return fmt.Errorf("agent: runner requires a Restorer")
	}
	return r.Config.Validate()
}

// RunningJob returns the currently executing backup job id, if any.
func (r *Runner) RunningJob() (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runningJob, r.runningJob != ""
}

// applyCancellations aborts best-effort any running job in ids and reports
// state=cancelled for it. Unknown ids are ignored.
func (r *Runner) applyCancellations(ctx context.Context, ids []string) {
	r.mu.Lock()
	job, cancel := r.runningJob, r.cancelRun
	rid, rcancel := r.runningR, r.cancelR
	r.mu.Unlock()
	for _, id := range ids {
		switch {
		case id != "" && id == job && cancel != nil:
			r.logger().Info("job cancelled by manager, aborting", "job_id", id)
			cancel()
		case id != "" && id == rid && rcancel != nil:
			r.logger().Info("restore cancelled by manager, aborting", "restore_id", id)
			rcancel()
		}
	}
	_ = ctx
}

// RunBackupLoop polls for jobs until ctx ends, executing one at a time.
func (r *Runner) RunBackupLoop(ctx context.Context) error {
	log := r.logger()
	interval := r.Config.PollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		timer.Reset(interval)
		resp, err := r.Client.PollJobs(ctx, 5)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Warn("job poll failed", "error", err)
			continue
		}
		r.applyCancellations(ctx, resp.CancelledJobIDs)
		for _, job := range resp.Jobs {
			if ctx.Err() != nil {
				return nil
			}
			if cancelled(resp.CancelledJobIDs, job.ID) {
				continue
			}
			if rec, ok := r.Store.Get(job.ID); ok && rec.Reported {
				continue // already terminally reported; never double-report
			}
			r.runOneJob(ctx, job)
		}
	}
}

func cancelled(ids []string, id string) bool {
	for _, c := range ids {
		if c == id {
			return true
		}
	}
	return false
}

// runOneJob executes, uploads, and reports a single job. Cancellation via
// cancelled_job_ids (or shutdown) reports state=cancelled best-effort.
func (r *Runner) runOneJob(ctx context.Context, job PolledJob) {
	log := r.logger().With("job_id", job.ID)
	r.workMu.Lock()
	defer r.workMu.Unlock()

	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	r.runningJob, r.cancelRun = job.ID, cancel
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.runningJob, r.cancelRun = "", nil
		r.mu.Unlock()
		cancel()
	}()
	// interrupted reports whether the run stopped due to cancellation or
	// shutdown rather than a job error: manager cancel -> report cancelled;
	// shutdown -> persist and stay silent (the manager re-dispatches).
	interrupted := func() (cancelled, shutdown bool) {
		if parent.Err() != nil {
			return false, true
		}
		return ctx.Err() == context.Canceled, false
	}

	rec := JobRecord{
		JobID: job.ID, Policy: job.PolicyID, PolicyVersion: job.PolicyVersion,
		State: JobRunning, Attempt: job.Attempt + 1,
		KeyRef: job.Encryption.KeyRef, Encrypted: job.EncryptionEnabled(),
		TargetsTotal: len(job.Targets),
	}
	_ = r.Store.Upsert(rec)
	r.reportProgress(ctx, job.ID, JobRunning, 0, 0)

	exec := *r.Executor
	exec.OnTargetComplete = func(done, total int, art *Artifact) {
		rec.TargetsDone = done
		rec.BytesRead += uint64(art.BytesRead)
		_ = r.Store.Upsert(rec)
		r.reportProgress(ctx, job.ID, JobRunning, rec.BytesRead, rec.BytesUploaded)
	}
	arts, execErr := exec.ExecuteJob(ctx, job)
	if execErr != nil {
		if cancelled, shutdown := interrupted(); shutdown {
			_ = r.Store.Upsert(rec) // re-run on restart; never report here
			exec.Cleanup(job.ID)
			return
		} else if cancelled || errors.Is(execErr, context.Canceled) {
			r.finishCancelled(ctx, job.ID, rec)
			exec.Cleanup(job.ID)
			return
		}
		r.finishFailed(ctx, job.ID, rec, execErr)
		exec.Cleanup(job.ID)
		return
	}

	rec.State = JobUploading
	_ = r.Store.Upsert(rec)
	up := *r.Uploader
	up.OnProgress = func(n int64) {
		rec.BytesUploaded += uint64(n)
		_ = r.Store.Upsert(rec)
		r.reportProgress(ctx, job.ID, JobUploading, rec.BytesRead, rec.BytesUploaded)
	}
	uploaded, upErr := uploadWithManifest(ctx, job, r.Config.AgentID, &exec, &up, arts)
	if upErr != nil {
		if cancelled, shutdown := interrupted(); shutdown {
			_ = r.Store.Upsert(rec)
			exec.Cleanup(job.ID)
			return
		} else if cancelled || errors.Is(upErr, context.Canceled) {
			r.finishCancelled(ctx, job.ID, rec)
			exec.Cleanup(job.ID)
			return
		}
		r.finishFailed(ctx, job.ID, rec, upErr)
		exec.Cleanup(job.ID)
		return
	}

	manifest := buildManifest(job, arts, uploaded, rec.BytesRead, rec.BytesUploaded, r.Config.CompressionLevel)
	rec.State = JobSucceeded
	rec.Reported = true
	for _, u := range uploaded {
		rec.UploadedObjects = append(rec.UploadedObjects, u.Key)
	}
	_ = r.Store.Upsert(rec)
	rep := JobReportPayload{JobID: job.ID, State: JobSucceeded, Manifest: &manifest}
	br, bu := rec.BytesRead, rec.BytesUploaded
	rep.BytesRead, rep.BytesUploaded = &br, &bu
	if err := r.Client.ReportJob(context.WithoutCancel(ctx), rep); err != nil {
		log.Warn("success report failed (job data is uploaded; manager will keep it dispatchable)", "error", err)
		rec.Reported = false
		rec.LastError = "report failed: " + err.Error()
		_ = r.Store.Upsert(rec)
		exec.Cleanup(job.ID)
		return
	}
	// The report only proves upload + acceptance: the manager verifies the
	// manifest and decides the job outcome (a 200 with verified:false still
	// lands here, by design, and must not read as success).
	log.Info("job uploaded, report accepted", "objects", len(uploaded))
	exec.Cleanup(job.ID)
}

func (r *Runner) finishFailed(ctx context.Context, jobID string, rec JobRecord, err error) {
	rec.State = JobFailed
	rec.LastError = err.Error()
	rec.Reported = true
	_ = r.Store.Upsert(rec)
	rep := JobReportPayload{JobID: jobID, State: JobFailed, Error: err.Error()}
	br, bu := rec.BytesRead, rec.BytesUploaded
	rep.BytesRead, rep.BytesUploaded = &br, &bu
	if rerr := r.Client.ReportJob(context.WithoutCancel(ctx), rep); rerr != nil {
		rec.Reported = false
		_ = r.Store.Upsert(rec)
		r.logger().Warn("failure report failed", "job_id", jobID, "error", rerr)
		return
	}
	r.logger().Info("job failed", "job_id", jobID, "error", err)
}

func (r *Runner) finishCancelled(ctx context.Context, jobID string, rec JobRecord) {
	rec.State = JobCancelled
	rec.Reported = true
	_ = r.Store.Upsert(rec)
	rep := JobReportPayload{JobID: jobID, State: JobCancelled, Error: "cancelled by manager"}
	br, bu := rec.BytesRead, rec.BytesUploaded
	rep.BytesRead, rep.BytesUploaded = &br, &bu
	if rerr := r.Client.ReportJob(context.WithoutCancel(ctx), rep); rerr != nil {
		rec.Reported = false
		_ = r.Store.Upsert(rec)
		r.logger().Warn("cancel report failed", "job_id", jobID, "error", rerr)
		return
	}
	r.logger().Info("job cancelled", "job_id", jobID)
}

func (r *Runner) reportProgress(ctx context.Context, jobID string, state JobState, read, uploaded uint64) {
	rep := JobReportPayload{JobID: jobID, State: state}
	br, bu := read, uploaded
	rep.BytesRead, rep.BytesUploaded = &br, &bu
	if err := r.Client.ReportJob(ctx, rep); err != nil {
		r.logger().Debug("progress report failed", "job_id", jobID, "error", err)
	}
}

// buildManifest assembles the completion manifest. Success is reported only
// when every artifact uploaded, so objects always covers all artifacts.
func buildManifest(job PolledJob, arts []*Artifact, uploaded []UploadedObject, read, up uint64, level int) Manifest {
	if level < 1 || level > 22 {
		level = 3
	}
	m := Manifest{
		Objects:       make([]ObjectManifest, 0, len(uploaded)),
		BytesRead:     read,
		BytesUploaded: up,
		Compression:   CompressionInfo{Algo: "zstd", Level: level},
		Encryption:    ManifestEncryption{Enabled: job.EncryptionEnabled(), KeyRef: job.Encryption.KeyRef},
	}
	for _, u := range uploaded {
		m.Objects = append(m.Objects, ObjectManifest{Key: u.Key, SizeBytes: u.Artifact.SizeBytes, SHA256: u.Artifact.SHA256})
		m.BytesCompressed += uint64(u.Artifact.BytesCompressed)
	}
	_ = arts
	return m
}

// RunRestoreLoop polls for restores until ctx ends, executing one at a time.
func (r *Runner) RunRestoreLoop(ctx context.Context) error {
	log := r.logger()
	interval := r.Config.RestorePollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		timer.Reset(interval)
		job, err := r.Client.PollRestore(ctx, r.Config.AgentID)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Warn("restore poll failed", "error", err)
			continue
		}
		if job == nil {
			continue
		}
		r.runOneRestore(ctx, *job)
	}
}

func (r *Runner) runOneRestore(ctx context.Context, job RestoreJob) {
	r.workMu.Lock()
	defer r.workMu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	r.runningR, r.cancelR = job.ID, cancel
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.runningR, r.cancelR = "", nil
		r.mu.Unlock()
		cancel()
	}()
	state, err := r.Restorer.Execute(ctx, job)
	if err != nil {
		r.logger().Info("restore finished", "restore_id", job.ID, "state", state, "error", err)
		return
	}
	r.logger().Info("restore finished", "restore_id", job.ID, "state", state)
}
