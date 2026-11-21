package restore

import (
	"time"

	"github.com/vaultix/vaultix/internal/domain"
	restoreuc "github.com/vaultix/vaultix/internal/usecase/restore"
)

// Encryption labels for the catalog (PRD §7.11: unencrypted snapshots must
// be clearly labeled as unencrypted).
const (
	EncryptionLabelEncrypted   = "encrypted"
	EncryptionLabelUnencrypted = "unencrypted"
)

// SnapshotDTO is one catalog entry with its encryption label and
// restorable flag.
type SnapshotDTO struct {
	ID            string  `json:"id"`
	AgentID       string  `json:"agent_id"`
	PolicyID      string  `json:"policy_id"`
	PolicyVersion int     `json:"policy_version"`
	JobID         string  `json:"job_id"`
	TargetType    string  `json:"target_type"`
	Selector      string  `json:"selector"`
	Status        string  `json:"status"`
	BytesOriginal *uint64 `json:"bytes_original,omitempty"`
	BytesStored   *uint64 `json:"bytes_stored,omitempty"`
	ObjectCount   int     `json:"object_count"`
	// Encryption is "encrypted" or "unencrypted" (never empty).
	Encryption string `json:"encryption"`
	KeyRef     string `json:"key_ref,omitempty"`
	ManifestID string `json:"manifest_id"`
	StorageID  string `json:"storage_id"`
	// Restorable is true only for verified snapshots with manifests.
	Restorable  bool       `json:"restorable"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// toSnapshotDTO converts a snapshot, deriving its encryption label and
// restorable flag.
func toSnapshotDTO(s *domain.Snapshot) SnapshotDTO {
	label := EncryptionLabelUnencrypted
	if s.Encrypted {
		label = EncryptionLabelEncrypted
	}
	return SnapshotDTO{
		ID: s.ID, AgentID: s.AgentID, PolicyID: s.PolicyID,
		PolicyVersion: s.PolicyVersion, JobID: s.JobID,
		TargetType: string(s.TargetType), Selector: s.Selector,
		Status: string(s.Status), BytesOriginal: s.BytesOriginal,
		BytesStored: s.BytesStored, ObjectCount: s.ObjectCount,
		Encryption: label, KeyRef: s.KeyRef, ManifestID: s.ManifestID,
		StorageID: s.StorageID, Restorable: s.IsRestorable(),
		CompletedAt: s.CompletedAt, CreatedAt: s.CreatedAt,
	}
}

// ManifestDTO carries manifest metadata plus its object set.
type ManifestDTO struct {
	ID            string      `json:"id"`
	SnapshotID    string      `json:"snapshot_id"`
	FormatVersion int         `json:"format_version"`
	ObjectKey     string      `json:"object_key"`
	Checksum      string      `json:"checksum"`
	Encryption    string      `json:"encryption"`
	KeyRef        string      `json:"key_ref,omitempty"`
	ObjectCount   int         `json:"object_count"`
	BytesStored   uint64      `json:"bytes_stored"`
	VerifiedAt    *time.Time  `json:"verified_at,omitempty"`
	CreatedAt     time.Time   `json:"created_at"`
	Objects       []ObjectDTO `json:"objects"`
}

// ObjectDTO is one manifest-referenced stored artifact.
type ObjectDTO struct {
	ID        string `json:"id"`
	ObjectKey string `json:"object_key"`
	Kind      string `json:"kind"`
	Size      uint64 `json:"size"`
	Checksum  string `json:"checksum"`
	Sequence  int    `json:"sequence"`
}

func toManifestDTO(m *domain.SnapshotManifest, objects []domain.SnapshotObject) ManifestDTO {
	label := EncryptionLabelUnencrypted
	if m.Encrypted {
		label = EncryptionLabelEncrypted
	}
	dto := ManifestDTO{
		ID: m.ID, SnapshotID: m.SnapshotID, FormatVersion: m.FormatVersion,
		ObjectKey: m.ObjectKey, Checksum: m.Checksum, Encryption: label,
		KeyRef: m.KeyRef, ObjectCount: m.ObjectCount, BytesStored: m.BytesStored,
		VerifiedAt: m.VerifiedAt, CreatedAt: m.CreatedAt, Objects: []ObjectDTO{},
	}
	for _, o := range objects {
		dto.Objects = append(dto.Objects, ObjectDTO{
			ID: o.ID, ObjectKey: o.ObjectKey, Kind: o.Kind,
			Size: o.Size, Checksum: o.Checksum, Sequence: o.Sequence,
		})
	}
	return dto
}

// CatalogPageDTO is a paginated catalog listing.
type CatalogPageDTO struct {
	Items  []SnapshotDTO `json:"items"`
	Total  int           `json:"total"`
	Limit  int           `json:"limit"`
	Offset int           `json:"offset"`
}

// SnapshotDetailDTO pairs a snapshot with its manifest (when present).
type SnapshotDetailDTO struct {
	Snapshot SnapshotDTO  `json:"snapshot"`
	Manifest *ManifestDTO `json:"manifest,omitempty"`
}

// PlanRequestDTO asks for a restore plan.
type PlanRequestDTO struct {
	Kind          string   `json:"kind"`
	SnapshotID    string   `json:"snapshot_id,omitempty"`
	SnapshotIDs   []string `json:"snapshot_ids,omitempty"`
	Engine        string   `json:"engine,omitempty"`
	Paths         []string `json:"paths,omitempty"`
	Destination   string   `json:"destination,omitempty"`
	TargetDB      string   `json:"target_db,omitempty"`
	Order         []string `json:"order,omitempty"`
	TargetAgentID string   `json:"target_agent_id,omitempty"`
	RequestedBy   string   `json:"requested_by"`
}

// PlanDTO is a restore plan with steps, warnings, and compatibility notes.
type PlanDTO struct {
	ID                   string    `json:"id"`
	Kind                 string    `json:"kind"`
	AgentID              string    `json:"agent_id"`
	SnapshotIDs          []string  `json:"snapshot_ids"`
	Steps                []StepDTO `json:"steps"`
	Warnings             []string  `json:"warnings"`
	Compatibility        []string  `json:"compatibility,omitempty"`
	AlternateDestination bool      `json:"alternate_destination"`
	Paths                []string  `json:"paths,omitempty"`
	RequestedBy          string    `json:"requested_by"`
	CreatedAt            int64     `json:"created_at"`
	ExpiresAt            int64     `json:"expires_at"`
}

// StepDTO is one ordered restore action.
type StepDTO struct {
	Sequence    int      `json:"sequence"`
	Kind        string   `json:"kind"`
	SnapshotID  string   `json:"snapshot_id"`
	Source      string   `json:"source"`
	Destination string   `json:"destination"`
	Notes       []string `json:"notes,omitempty"`
}

func toPlanDTO(p *restoreuc.Plan) PlanDTO {
	dto := PlanDTO{
		ID: p.ID, Kind: string(p.Kind), AgentID: p.AgentID,
		SnapshotIDs: p.SnapshotIDs, Warnings: p.Warnings,
		Compatibility:        p.Compatibility,
		AlternateDestination: p.AlternateDestination, Paths: p.Paths,
		RequestedBy: p.RequestedBy, CreatedAt: p.CreatedAt, ExpiresAt: p.ExpiresAt,
		Steps: []StepDTO{},
	}
	if dto.Warnings == nil {
		dto.Warnings = []string{}
	}
	if dto.SnapshotIDs == nil {
		dto.SnapshotIDs = []string{}
	}
	for _, s := range p.Steps {
		dto.Steps = append(dto.Steps, StepDTO{
			Sequence: s.Sequence, Kind: string(s.Kind), SnapshotID: s.SnapshotID,
			Source: s.Source, Destination: s.Destination, Notes: s.Notes,
		})
	}
	return dto
}

// ConfirmRequestDTO binds a plan to a single-use token.
type ConfirmRequestDTO struct {
	PlanID string `json:"plan_id"`
	Actor  string `json:"actor"`
}

// ConfirmationDTO returns the single-use token.
type ConfirmationDTO struct {
	Token     string    `json:"token"`
	PlanID    string    `json:"plan_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ExecuteRequestDTO consumes a confirmation token.
type ExecuteRequestDTO struct {
	Token string `json:"token"`
	Actor string `json:"actor"`
}

// RestoreJobDTO tracks a restore job.
type RestoreJobDTO struct {
	ID           string    `json:"id"`
	AgentID      string    `json:"agent_id"`
	SnapshotID   string    `json:"snapshot_id"`
	TargetPath   string    `json:"target_path,omitempty"`
	Paths        []string  `json:"paths,omitempty"`
	State        string    `json:"state"`
	Progress     *float64  `json:"progress,omitempty"`
	ErrorCode    string    `json:"error_code,omitempty"`
	ErrorMessage string    `json:"error_message,omitempty"`
	ApprovedBy   string    `json:"approved_by,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func toRestoreJobDTO(j *domain.RestoreJob) RestoreJobDTO {
	return RestoreJobDTO{
		ID: j.ID, AgentID: j.AgentID, SnapshotID: j.SnapshotID,
		TargetPath: j.TargetPath, Paths: j.Paths, State: string(j.State),
		Progress: j.Progress, ErrorCode: j.ErrorCode, ErrorMessage: j.ErrorMessage,
		ApprovedBy: j.ApprovedBy, CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt,
	}
}
