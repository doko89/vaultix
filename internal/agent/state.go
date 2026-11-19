package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// JobState mirrors PRD §7.10 required job states.
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

const stateFileVersion = 1

// JobRecord is the minimal local job state kept for restart recovery.
// It never carries key material: KeyRef only. Resume is re-run from scratch
// (uploads overwrite the same manager-issued keys, so it is idempotent);
// success is reported exactly once, at the end of a completed run.
type JobRecord struct {
	JobID           string    `json:"job_id"`
	Policy          string    `json:"policy"`
	PolicyVersion   int       `json:"policy_version"`
	State           JobState  `json:"state"`
	Attempt         int       `json:"attempt"`
	LastError       string    `json:"last_error,omitempty"`
	KeyRef          string    `json:"key_ref,omitempty"`
	Encrypted       bool      `json:"encrypted,omitempty"`
	TargetsDone     int       `json:"targets_done,omitempty"`
	TargetsTotal    int       `json:"targets_total,omitempty"`
	BytesRead       uint64    `json:"bytes_read,omitempty"`
	BytesUploaded   uint64    `json:"bytes_uploaded,omitempty"`
	UploadedObjects []string  `json:"uploaded_objects,omitempty"`
	Reported        bool      `json:"reported,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type stateFile struct {
	Version int                  `json:"version"`
	Jobs    map[string]JobRecord `json:"jobs"`
}

// Store persists job records to a JSON file. Writes are crash-safe
// (temp file + fsync + rename) and the file is owner-only (0600).
type Store struct {
	mu   sync.Mutex
	path string
	jobs map[string]JobRecord
}

// NewStore opens the state file at path, loading existing records when the
// file exists. A missing file yields an empty store.
func NewStore(path string) (*Store, error) {
	s := &Store{path: path, jobs: map[string]JobRecord{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("agent: read state file: %w", err)
	}
	var sf stateFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return nil, fmt.Errorf("agent: decode state file: %w", err)
	}
	if sf.Version != stateFileVersion {
		return nil, fmt.Errorf("agent: unsupported state version %d", sf.Version)
	}
	if sf.Jobs != nil {
		s.jobs = sf.Jobs
	}
	return s, nil
}

// Path returns the backing file path.
func (s *Store) Path() string { return s.path }

// Upsert inserts or replaces a record and persists the store.
func (s *Store) Upsert(rec JobRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec.UpdatedAt = time.Now().UTC()
	s.jobs[rec.JobID] = rec
	return s.saveLocked()
}

// Get returns the record for jobID.
func (s *Store) Get(jobID string) (JobRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.jobs[jobID]
	return rec, ok
}

// Delete removes a record and persists the store.
func (s *Store) Delete(jobID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.jobs, jobID)
	return s.saveLocked()
}

// All returns a snapshot of every record.
func (s *Store) All() []JobRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]JobRecord, 0, len(s.jobs))
	for _, rec := range s.jobs {
		out = append(out, rec)
	}
	return out
}

// Incomplete returns records that did not reach a terminal state; callers use
// this after a restart to resume or safely requeue work.
func (s *Store) Incomplete() []JobRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []JobRecord
	for _, rec := range s.jobs {
		switch rec.State {
		case JobSucceeded, JobFailed, JobCancelled:
		default:
			out = append(out, rec)
		}
	}
	return out
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("agent: create state dir: %w", err)
	}
	raw, err := json.MarshalIndent(stateFile{Version: stateFileVersion, Jobs: s.jobs}, "", "  ")
	if err != nil {
		return fmt.Errorf("agent: encode state: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".agent-state-*.tmp")
	if err != nil {
		return fmt.Errorf("agent: create temp state: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup; a crash may leave a temp file behind, which is
	// harmless because the real file is only ever replaced by rename.
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("agent: write temp state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("agent: sync temp state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("agent: close temp state: %w", err)
	}
	//nolint:gosec // state may reference job metadata; keep it owner-only.
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return fmt.Errorf("agent: chmod temp state: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("agent: install state: %w", err)
	}
	return nil
}
