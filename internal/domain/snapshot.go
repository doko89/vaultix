package domain

import "time"

// SnapshotStatus tracks verification state. A snapshot is restorable only
// after its manifest and required objects pass integrity checks (PRD §7.13).
type SnapshotStatus string

const (
	SnapshotPending   SnapshotStatus = "pending"   // artifacts still uploading
	SnapshotVerifying SnapshotStatus = "verifying" // integrity checks running
	SnapshotVerified  SnapshotStatus = "verified"  // restorable
	SnapshotFailed    SnapshotStatus = "failed"    // incomplete or corrupt
	SnapshotExpired   SnapshotStatus = "expired"   // pruned by retention
)

// Snapshot is a completed backup catalog entry (PRD `snapshots`).
type Snapshot struct {
	ID            string
	AgentID       string
	PolicyID      string
	PolicyVersion int
	JobID         string
	TargetType    TargetType
	// Selector echoes the policy target selector for catalog browsing.
	Selector string

	Status SnapshotStatus

	BytesOriginal *uint64
	BytesStored   *uint64
	ObjectCount   int
	Encrypted     bool
	KeyRef        string // set when Encrypted
	ManifestID    string
	StorageID     string
	CompletedAt   *time.Time
	ExpiresAt     *time.Time // retention-computed; nil = keep per policy
	CreatedAt     time.Time
}

// IsRestorable reports whether the snapshot may be offered for restore.
func (s Snapshot) IsRestorable() bool {
	return s.Status == SnapshotVerified && s.ManifestID != ""
}

// SnapshotManifest describes one snapshot's object set and integrity data
// (PRD `snapshot_manifests`). It records whether the backup is encrypted
// and the key reference needed for restore.
type SnapshotManifest struct {
	ID         string
	SnapshotID string
	// FormatVersion pins the manifest schema for future evolution.
	FormatVersion int
	// ObjectKey is the storage key of the manifest artifact itself.
	ObjectKey string
	// Checksum of the manifest artifact (algorithm:hex).
	Checksum string
	// Encrypted mirrors the snapshot flag; unencrypted snapshots are
	// labeled as such in the catalog.
	Encrypted   bool
	KeyRef      string
	ObjectCount int
	BytesStored uint64
	VerifiedAt  *time.Time
	CreatedAt   time.Time
}

// SnapshotObject is one stored artifact referenced by a manifest
// (PRD `snapshot_objects`): data chunk, dump artifact, or metadata file.
type SnapshotObject struct {
	ID         string
	ManifestID string
	SnapshotID string
	// ObjectKey is the storage key within the destination bucket/prefix.
	ObjectKey string
	// Kind is informational: chunk, dump, metadata, manifest.
	Kind string
	Size uint64
	// Checksum (algorithm:hex); verified before success is recorded.
	Checksum string
	// Sequence orders chunks for reassembly.
	Sequence  int
	CreatedAt time.Time
}
