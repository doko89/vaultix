package domain

import (
	"errors"
	"testing"
	"time"
)

func TestJobTransitions(t *testing.T) {
	happy := []JobState{JobQueued, JobAssigned, JobRunning, JobUploading, JobVerifying, JobSucceeded}
	s := JobQueued
	for _, next := range happy[1:] {
		if err := TransitionTo(&s, next); err != nil {
			t.Fatalf("transition to %s failed: %v", next, err)
		}
	}
	if !s.Terminal() {
		t.Fatal("succeeded must be terminal")
	}
	if err := TransitionTo(&s, JobQueued); !errors.Is(err, ErrConflict) {
		t.Fatalf("succeeded->queued: want ErrConflict, got %v", err)
	}

	// Retry path: failed -> queued, then full run again.
	s = JobRunning
	if err := TransitionTo(&s, JobFailed); err != nil {
		t.Fatalf("running->failed: %v", err)
	}
	if err := TransitionTo(&s, JobQueued); err != nil {
		t.Fatalf("failed->queued (retry): %v", err)
	}

	// Cancel from every non-terminal state.
	for _, from := range []JobState{JobQueued, JobAssigned, JobRunning, JobUploading, JobVerifying} {
		s = from
		if err := TransitionTo(&s, JobCancelled); err != nil {
			t.Fatalf("%s->cancelled: %v", from, err)
		}
	}

	// Illegal jumps.
	for _, tc := range [][2]JobState{
		{JobQueued, JobRunning},
		{JobQueued, JobSucceeded},
		{JobRunning, JobSucceeded},
		{JobFailed, JobSucceeded}, // retry must requeue, never fabricate success
		{JobCancelled, JobQueued},
	} {
		s = tc[0]
		if err := TransitionTo(&s, tc[1]); !errors.Is(err, ErrConflict) {
			t.Fatalf("%s->%s: want ErrConflict, got %v", tc[0], tc[1], err)
		}
	}
}

func TestCompressionRatio(t *testing.T) {
	read, comp := uint64(1000), uint64(250)
	j := BackupJob{BytesRead: &read, BytesCompressed: &comp}
	r := j.CompressionRatio()
	if r == nil || *r != 0.25 {
		t.Fatalf("want ratio 0.25, got %v", r)
	}
	if (BackupJob{}).CompressionRatio() != nil {
		t.Fatal("missing byte counts must yield nil ratio")
	}
	zero := uint64(0)
	if (BackupJob{BytesRead: &zero, BytesCompressed: &comp}).CompressionRatio() != nil {
		t.Fatal("zero bytes read must yield nil ratio")
	}
}

func TestSnapshotIsRestorable(t *testing.T) {
	if !(Snapshot{Status: SnapshotVerified, ManifestID: "m1"}).IsRestorable() {
		t.Fatal("verified snapshot with manifest must be restorable")
	}
	for _, s := range []Snapshot{
		{Status: SnapshotPending, ManifestID: "m1"},
		{Status: SnapshotVerifying, ManifestID: "m1"},
		{Status: SnapshotFailed, ManifestID: "m1"},
		{Status: SnapshotExpired, ManifestID: "m1"},
		{Status: SnapshotVerified}, // no manifest
	} {
		if s.IsRestorable() {
			t.Fatalf("snapshot %+v must not be restorable", s)
		}
	}
}

func TestScheduleValidate(t *testing.T) {
	wd := time.Wednesday
	runAt := time.Now().Add(time.Hour)
	valid := []Schedule{
		{PolicyID: "p", Kind: ScheduleOneTime, RunAt: &runAt, Timezone: "UTC"},
		{PolicyID: "p", Kind: ScheduleHourly, IntervalHours: 6, Timezone: "Asia/Jakarta"},
		{PolicyID: "p", Kind: ScheduleDaily, TimeOfDay: "02:30", Timezone: "UTC"},
		{PolicyID: "p", Kind: ScheduleWeekly, TimeOfDay: "03:00", Weekday: &wd, Timezone: "UTC"},
		{PolicyID: "p", Kind: ScheduleCron, CronExpr: "0 2 * * *", Timezone: "UTC"},
		{PolicyID: "p", Kind: ScheduleDisabled, Timezone: "UTC"},
	}
	for i, s := range valid {
		if err := s.Validate(); err != nil {
			t.Fatalf("valid[%d] rejected: %v", i, err)
		}
	}
	invalid := []Schedule{
		{Kind: ScheduleDaily, TimeOfDay: "02:30", Timezone: "UTC"}, // no policy
		{PolicyID: "p", Kind: ScheduleOneTime, Timezone: "UTC"},    // no run_at
		{PolicyID: "p", Kind: ScheduleHourly, Timezone: "UTC"},     // no interval
		{PolicyID: "p", Kind: ScheduleDaily, TimeOfDay: "25:99", Timezone: "UTC"},
		{PolicyID: "p", Kind: ScheduleWeekly, TimeOfDay: "03:00", Timezone: "UTC"}, // no weekday
		{PolicyID: "p", Kind: ScheduleCron, CronExpr: "0 2 *", Timezone: "UTC"},
		{PolicyID: "p", Kind: "minutely", Timezone: "UTC"},
		{PolicyID: "p", Kind: ScheduleDaily, TimeOfDay: "02:30", Timezone: "Mars/Olympus"},
		{PolicyID: "p", Kind: ScheduleDaily, TimeOfDay: "02:30", Timezone: "UTC", MaxConcurrency: -1},
	}
	for i, s := range invalid {
		if err := s.Validate(); !errors.Is(err, ErrValidation) {
			t.Fatalf("invalid[%d] (%+v): want ErrValidation, got %v", i, s, err)
		}
	}
}

func TestSchedulePauseResume(t *testing.T) {
	s := Schedule{Kind: ScheduleDaily}
	if !s.Active() {
		t.Fatal("daily schedule should be active")
	}
	s.Pause()
	if s.Active() {
		t.Fatal("paused schedule must be inactive")
	}
	s.Resume()
	if !s.Active() {
		t.Fatal("resumed schedule should be active")
	}
	if (!(Schedule{Kind: ScheduleDisabled}).Active()) == false {
		t.Fatal("disabled schedule must be inactive")
	}
}

func TestPolicyValidation(t *testing.T) {
	valid := BackupPolicy{Name: "daily-home", AgentIDs: []string{"a1"}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid policy rejected: %v", err)
	}
	enc := BackupPolicy{Name: "enc", AgentIDs: []string{"a1"}, Encryption: Encryption{Enabled: true}}
	if err := enc.Validate(); !errors.Is(err, ErrValidation) {
		t.Fatalf("encryption without key ref: want ErrValidation, got %v", err)
	}
	enc.Encryption.KeyRef = "sek_1"
	if err := enc.Validate(); err != nil {
		t.Fatalf("encrypted policy with key ref rejected: %v", err)
	}

	if err := (BackupPolicyTarget{Type: TargetPostgres, Selector: "appdb"}).Validate(); !errors.Is(err, ErrValidation) {
		t.Fatalf("db target without credential: want ErrValidation, got %v", err)
	}
	db := BackupPolicyTarget{Type: TargetPostgres, Selector: "appdb", CredentialRef: "sek_db"}
	if err := db.Validate(); err != nil {
		t.Fatalf("db target with credential rejected: %v", err)
	}
	if err := (BackupPolicyTarget{Type: "tape", Selector: "x"}).Validate(); !errors.Is(err, ErrValidation) {
		t.Fatalf("unknown target type: want ErrValidation, got %v", err)
	}
}

func TestRetentionRule(t *testing.T) {
	if err := (RetentionRule{KeepLastN: 7, KeepMinimum: 1}).Validate(); err != nil {
		t.Fatalf("valid retention rejected: %v", err)
	}
	if err := (RetentionRule{}).Validate(); !errors.Is(err, ErrValidation) {
		t.Fatalf("empty retention: want ErrValidation, got %v", err)
	}
	if err := (RetentionRule{KeepDays: -1}).Validate(); !errors.Is(err, ErrValidation) {
		t.Fatalf("negative retention: want ErrValidation, got %v", err)
	}
	r := RetentionRule{KeepLastN: 3, KeepDays: 30}
	if r.Applies(1, time.Hour) {
		t.Fatal("recent snapshot within bounds must be kept")
	}
	if !r.Applies(5, time.Hour) {
		t.Fatal("snapshot beyond keep_last_n must be pruned")
	}
	if !r.Applies(0, 40*24*time.Hour) {
		t.Fatal("snapshot older than keep_days must be pruned")
	}
}

func TestStorageAndSecretValidation(t *testing.T) {
	if err := (StorageDestination{Name: "minio", Bucket: "vaultix"}).Validate(); err != nil {
		t.Fatalf("valid destination rejected: %v", err)
	}
	if err := (StorageDestination{Name: "x"}).Validate(); !errors.Is(err, ErrValidation) {
		t.Fatalf("destination without bucket: want ErrValidation, got %v", err)
	}
	if err := (SecretProfile{Name: "pg", Kind: SecretKindDatabase, Ciphertext: []byte{1}}).Validate(); err != nil {
		t.Fatalf("valid secret rejected: %v", err)
	}
	if err := (SecretProfile{Name: "pg", Kind: SecretKindDatabase}).Validate(); !errors.Is(err, ErrValidation) {
		t.Fatalf("secret without ciphertext: want ErrValidation, got %v", err)
	}
}
