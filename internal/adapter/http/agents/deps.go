package agents

import (
	"context"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
	agentuc "github.com/vaultix/vaultix/internal/usecase/agent"
	updateuc "github.com/vaultix/vaultix/internal/usecase/agentupdate"
	discoveryuc "github.com/vaultix/vaultix/internal/usecase/discovery"
)

// JobEncryption is the per-job data-key envelope. It is always present on a
// queued job: Enabled/KeyRef echo the pinned policy version, and KeyB64
// carries the short-lived base64 data key when (and only when) encryption is
// enabled AND the transport is TLS. KeyB64 is never logged.
type JobEncryption struct {
	Enabled bool   `json:"enabled"`
	KeyRef  string `json:"key_ref,omitempty"`
	KeyB64  string `json:"key_b64,omitempty"`
}

// JobTarget is one structured backup target: type, selector, and the
// method-specific options frozen on the pinned policy version.
type JobTarget struct {
	Type     string            `json:"type"`
	Selector string            `json:"selector"`
	Options  map[string]string `json:"options"`
}

// DestinationCredential is the per-job direct-upload credential issued at
// poll for destinations with upload_mode "direct" (explicit per-destination
// opt-in exception to PRD §7.12 "never expose long-lived creds to agents";
// use only for S3 implementations with broken query auth). All seven fields
// are always present when the object is present. It is minted per poll over
// TLS only and must never be logged. Agents that predate direct mode ignore
// it (unknown JSON field) and fail loudly at the presigned-grant step,
// which direct destinations refuse.
type DestinationCredential struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Bucket          string `json:"bucket"`
	UsePathStyle    bool   `json:"use_path_style"`
	Prefix          string `json:"prefix"`
}

// QueuedJob is the dispatch slice of a job handed to a polling agent. It is
// intentionally narrow: the jobs track owns the full job model and maps it
// onto this shape.
type QueuedJob struct {
	ID            string        `json:"id"`
	PolicyID      string        `json:"policy_id"`
	PolicyVersion int           `json:"policy_version"`
	Kind          string        `json:"kind"`
	Targets       []JobTarget   `json:"targets"`
	State         string        `json:"state"`
	Attempt       int           `json:"attempt"`
	Encryption    JobEncryption `json:"encryption"`
	// UploadMode is "direct" when the job's resolved destination opts into
	// direct upload, in which case DestinationCredential carries the
	// per-job S3 credential. Both are omitted for presigned destinations
	// (and legacy rows without a mode), so the presigned poll shape is
	// byte-identical to before direct mode existed; agents treat an absent
	// mode as presigned.
	UploadMode            string                 `json:"upload_mode,omitempty"`
	DestinationCredential *DestinationCredential `json:"destination_credential,omitempty"`
}

// ManifestObject is one flat manifest entry: storage key, stored bytes, hex
// sha256 of the uploaded bytes.
type ManifestObject struct {
	Key       string
	SizeBytes uint64
	SHA256    string
}

// JobManifest is the agent's end-of-upload completion manifest. Present only
// on reports that drive verification + completion; nil means the legacy
// progress-only path.
type JobManifest struct {
	Objects         []ManifestObject
	BytesRead       *uint64
	BytesCompressed *uint64
	BytesUploaded   *uint64
	Compression     string
	Encrypted       *bool
	KeyRef          string
}

// JobReport is an agent's validated progress/outcome report for one job.
type JobReport struct {
	JobID         string
	State         domain.JobState
	Error         string
	BytesRead     *uint64
	BytesUploaded *uint64
	Manifest      *JobManifest
}

// CompletionOutcome is the result of a manifest-bearing report: whether the
// job verified (and thus succeeded), the snapshot it produced, and the
// actionable failure details when it did not.
type CompletionOutcome struct {
	Verified   bool
	SnapshotID string
	Failures   []string
}

// JobQueue is the narrow seam to the jobs track (NOT usecase/job, which the
// parallel track owns). The jobs-track service implements it; this package
// only polls dispatchable jobs and accepts reports.
type JobQueue interface {
	Poll(ctx context.Context, agentID string, limit int) ([]QueuedJob, error)
	// CancelledIDs returns jobs cancelled while assigned to agentID since
	// the last poll, so the agent stops in-flight work promptly.
	CancelledIDs(ctx context.Context, agentID string) ([]string, error)
	Report(ctx context.Context, agentID string, rep JobReport) error
	// CompleteWithManifest drives verify-then-complete for manifest-bearing
	// reports (rep.Manifest non-nil). The job succeeds only when verified.
	CompleteWithManifest(ctx context.Context, agentID string, rep JobReport) (*CompletionOutcome, error)
}

// UploadObjectRequest names one object the agent wants to upload: either a
// name relative to the job scope (e.g. "chunk-0001.zst") or the full scoped
// key. SizeBytes is advisory (validated sane, not enforced byte-exact).
type UploadObjectRequest struct {
	Key       string
	SizeBytes uint64
}

// ObjectGrant is a minted single-object presigned grant.
type ObjectGrant struct {
	Key       string            `json:"key"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers,omitempty"`
	ExpiresAt time.Time         `json:"expires_at"`
}

// MultipartPart identifies one uploaded part for completion. SHA256 is the
// bare hex sha256 of the exact part bytes the agent PUT; SizeBytes is that
// part's byte length. Both are required: the completion gate hash-verifies
// every part against them (a multipart ETag is an md5-of-md5s composite and
// can never be compared to a content hash).
type MultipartPart struct {
	PartNumber int
	ETag       string
	SHA256     string
	SizeBytes  int64
}

// GrantService mints short-lived, strictly job-scoped storage grants for
// agents. Every method verifies the job belongs to the authenticated agent
// and that every key stays inside that job's scope (or, for restore
// downloads, inside the restore's snapshot); violations are rejected, never
// widened.
type GrantService interface {
	// IssueUploadGrants mints one PUT grant per object, in request order.
	IssueUploadGrants(ctx context.Context, agentID, jobID string, objects []UploadObjectRequest) ([]ObjectGrant, error)
	// InitMultipartUpload starts a multipart upload and returns the FULL
	// part-URL list (part i uses partURLs[i-1]) for the given byte size
	// and part size. The agent then PUTs each part and completes/aborts.
	InitMultipartUpload(ctx context.Context, agentID, jobID, object string, sizeBytes uint64, partSize int64) (key, uploadID string, partURLs []string, err error)
	CompleteMultipartUpload(ctx context.Context, agentID, jobID, key, uploadID string, parts []MultipartPart) error
	AbortMultipartUpload(ctx context.Context, agentID, jobID, key, uploadID string) error
}

// RestoreDestination tells the agent where to put data back: mode is
// "original" (in place, with an overwrite warning) or "alternate", in which
// case Path carries the alternate destination.
type RestoreDestination struct {
	Mode string `json:"mode"`
	Path string `json:"path,omitempty"`
}

// RestoreObject is one snapshot object the agent must download: storage
// key, stored bytes, and bare hex sha256.
type RestoreObject struct {
	Key       string `json:"key"`
	SizeBytes uint64 `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

// RestoreEncryption is the per-restore data-key envelope. KeyB64 carries
// the short-lived base64 data key when (and only when) the snapshot is
// encrypted AND the transport is TLS. Never logged.
type RestoreEncryption struct {
	Enabled bool   `json:"enabled"`
	KeyRef  string `json:"key_ref,omitempty"`
	KeyB64  string `json:"key_b64,omitempty"`
}

// RestoreAssignment is one restore job handed to a polling agent.
type RestoreAssignment struct {
	ID              string             `json:"id"`
	SnapshotID      string             `json:"snapshot_id"`
	Kind            string             `json:"kind"`
	Destination     RestoreDestination `json:"destination"`
	OverwritePolicy string             `json:"overwrite_policy"`
	Objects         []RestoreObject    `json:"objects"`
	Paths           []string           `json:"paths,omitempty"`
	Encryption      RestoreEncryption  `json:"encryption"`
}

// RestoreReport is the agent's execution report for one restore job.
type RestoreReport struct {
	State domain.JobState
	Error string
}

// RestoreAgentService is the agent-facing restore seam: poll the next due
// restore, mint download grants confined to its snapshot, and track
// execution reports.
type RestoreAgentService interface {
	// PollRestore returns the agent's oldest unfinished restore job, or
	// (nil, nil) when none is due.
	PollRestore(ctx context.Context, agentID string) (*RestoreAssignment, error)
	IssueDownloadGrants(ctx context.Context, agentID, restoreID string, keys []string) ([]ObjectGrant, error)
	ReportRestore(ctx context.Context, agentID, restoreID string, rep RestoreReport) error
}

// AgentService mirrors the agent usecase surface this package needs. Deps
// holds it as an interface so handlers stay testable without a database.
type AgentService interface {
	IssueEnrollmentToken(ctx context.Context, ttl time.Duration) (string, *domain.AgentCredential, error)
	RedeemEnrollmentToken(ctx context.Context, token string, reg agentuc.Registration) (*domain.Agent, string, error)
	Heartbeat(ctx context.Context, in agentuc.HeartbeatInput) (*agentuc.HeartbeatResult, error)
	GetAgent(ctx context.Context, id string) (*agentuc.AgentView, error)
	ListAgents(ctx context.Context, opts domain.ListOptions, filter agentuc.AgentFilter) (*agentuc.AgentListResult, error)
	RevokeAgent(ctx context.Context, id string) error
	SetDisabled(ctx context.Context, id string, disabled bool) error
	DeleteAgent(ctx context.Context, id string) error
	AgentPolicies(ctx context.Context, agentID string) ([]agentuc.PolicySummary, error)
	AuthenticateAgent(ctx context.Context, bearer string) (*domain.AgentCredential, error)
}

// DiscoveryService mirrors the discovery usecase surface this package needs.
type DiscoveryService interface {
	Ingest(ctx context.Context, agentID string, resources []discoveryuc.ResourceInput) (int, error)
	List(ctx context.Context, agentID string, kind domain.ResourceKind) ([]domain.DiscoveredResource, error)
}

// UpdateService mirrors the agent-update usecase surface this package
// needs. It is optional like Grants/Restores: nil answers 501 on the
// update endpoints and omits the poll directive.
type UpdateService interface {
	UploadArtifact(ctx context.Context, version, goos, goarch string, data []byte, by string) (*domain.AgentArtifact, error)
	ListArtifacts(ctx context.Context) ([]domain.AgentArtifact, error)
	DeleteArtifact(ctx context.Context, id string) error
	RequestUpdate(ctx context.Context, agentID, artifactID, by string) (*domain.AgentUpdate, error)
	PendingDirective(ctx context.Context, agentID string) (*updateuc.Directive, error)
	DownloadArtifact(ctx context.Context, agentID string) (*domain.AgentArtifact, error)
	ReportUpdate(ctx context.Context, agentID, status, version, errMsg string) error
	Describe(ctx context.Context, agentID string) (*updateuc.Info, error)
}

// Compile-time proof that the real usecase services satisfy the seams.
var (
	_ AgentService     = (*agentuc.Service)(nil)
	_ DiscoveryService = (*discoveryuc.Service)(nil)
	_ UpdateService    = (*updateuc.Service)(nil)
)
