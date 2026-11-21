package agents

import (
	"encoding/json"
	"time"
)

// Every request DTO is validated at the boundary (validator tags plus manual
// checks in the handlers). Responses never carry plaintext credentials
// except the single-issuance token/enroll responses, which are shown once.

type issueTokenRequest struct {
	// TTLHours defaults to 24 when 0; max 168 (7d).
	TTLHours int `json:"ttl_hours" validate:"gte=0,lte=168"`
}

type issueTokenResponse struct {
	Token        string    `json:"token"`
	CredentialID string    `json:"credential_id"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type agentSummary struct {
	ID              string     `json:"id"`
	Hostname        string     `json:"hostname"`
	IPAddress       string     `json:"ip_address,omitempty"`
	OS              string     `json:"os,omitempty"`
	Architecture    string     `json:"architecture,omitempty"`
	Version         string     `json:"version,omitempty"`
	Capabilities    []string   `json:"capabilities,omitempty"`
	Disabled        bool       `json:"disabled"`
	Status          string     `json:"status"`
	Outdated        bool       `json:"outdated"`
	LastHeartbeatAt *time.Time `json:"last_heartbeat_at,omitempty"`
	DiskTotalBytes  *uint64    `json:"disk_total_bytes,omitempty"`
	DiskFreeBytes   *uint64    `json:"disk_free_bytes,omitempty"`
	EnrolledAt      time.Time  `json:"enrolled_at"`
}

type listAgentsResponse struct {
	Items []agentSummary `json:"items"`
	Total int            `json:"total"`
}

type patchAgentRequest struct {
	Disabled *bool `json:"disabled"`
}

type policySummary struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Enabled        bool   `json:"enabled"`
	CurrentVersion int    `json:"current_version"`
}

type agentPoliciesResponse struct {
	AgentID  string          `json:"agent_id"`
	Policies []policySummary `json:"policies"`
}

type resourceDTO struct {
	ID           string            `json:"id"`
	Kind         string            `json:"kind"`
	Name         string            `json:"name"`
	Detail       map[string]string `json:"detail,omitempty"`
	DiscoveredAt time.Time         `json:"discovered_at"`
	LastSeenAt   time.Time         `json:"last_seen_at"`
}

type agentResourcesResponse struct {
	AgentID string        `json:"agent_id"`
	Items   []resourceDTO `json:"items"`
}

// --- Agent protocol DTOs ---

type enrollRequest struct {
	Token        string   `json:"token" validate:"required"`
	Hostname     string   `json:"hostname" validate:"required,max=255"`
	IPAddress    string   `json:"ip_address" validate:"max=64"`
	OS           string   `json:"os" validate:"max=64"`
	Architecture string   `json:"architecture" validate:"max=32"`
	Version      string   `json:"version" validate:"max=64"`
	Capabilities []string `json:"capabilities" validate:"max=64,dive,max=64"`
}

type enrollResponse struct {
	AgentID    string    `json:"agent_id"`
	Token      string    `json:"token"`
	EnrolledAt time.Time `json:"enrolled_at"`
}

type heartbeatRequest struct {
	Version         string   `json:"version" validate:"max=64"`
	OS              string   `json:"os" validate:"max=64"`
	Architecture    string   `json:"architecture" validate:"max=32"`
	IPAddress       string   `json:"ip_address" validate:"max=64"`
	DiskTotalBytes  *uint64  `json:"disk_total_bytes,omitempty"`
	DiskFreeBytes   *uint64  `json:"disk_free_bytes,omitempty"`
	RunningJobs     int      `json:"running_jobs" validate:"gte=0,lte=100000"`
	DockerAvailable bool     `json:"docker_available"`
	Capabilities    []string `json:"capabilities" validate:"max=64,dive,max=64"`
}

type heartbeatResponse struct {
	OK         bool      `json:"ok"`
	Status     string    `json:"status"`
	Outdated   bool      `json:"outdated"`
	ServerTime time.Time `json:"server_time"`
}

type pollResponse struct {
	Jobs            []QueuedJob      `json:"jobs"`
	CancelledJobIDs []string         `json:"cancelled_job_ids"`
	Update          *updateDirective `json:"update,omitempty"`
	ServerTime      time.Time        `json:"server_time"`
}

type reportManifestObjectDTO struct {
	Key       string `json:"key" validate:"required,max=1024"`
	SizeBytes uint64 `json:"size_bytes"`
	SHA256    string `json:"sha256" validate:"required,max=128"`
}

type reportManifestEncryptionDTO struct {
	Enabled bool   `json:"enabled"`
	KeyRef  string `json:"key_ref,omitempty" validate:"max=256"`
}

type reportManifestDTO struct {
	Objects         []reportManifestObjectDTO    `json:"objects" validate:"required,min=1,max=20000,dive"`
	BytesRead       *uint64                      `json:"bytes_read,omitempty"`
	BytesCompressed *uint64                      `json:"bytes_compressed,omitempty"`
	BytesUploaded   *uint64                      `json:"bytes_uploaded,omitempty"`
	Compression     compressionDTO               `json:"compression,omitempty" validate:"max=32"`
	Encryption      *reportManifestEncryptionDTO `json:"encryption,omitempty"`
}

// compressionDTO accepts the agent's compression record either as a bare
// string ("zstd") or as an object ({"algo":"zstd","level":3}). It is
// informational only (the pinned policy version owns the record), so the
// object form keeps the algorithm name.
type compressionDTO string

// UnmarshalJSON decodes the string form directly and the object form via
// its algo field; anything else is a validation error at the boundary.
func (c *compressionDTO) UnmarshalJSON(raw []byte) error {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		*c = compressionDTO(s)
		return nil
	}
	var obj struct {
		Algo  string `json:"algo"`
		Level int    `json:"level"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return err
	}
	*c = compressionDTO(obj.Algo)
	return nil
}

type reportJobDTO struct {
	JobID         string             `json:"job_id" validate:"required,max=128"`
	State         string             `json:"state" validate:"required,max=32"`
	Error         string             `json:"error" validate:"max=4096"`
	BytesRead     *uint64            `json:"bytes_read,omitempty"`
	BytesUploaded *uint64            `json:"bytes_uploaded,omitempty"`
	Manifest      *reportManifestDTO `json:"manifest,omitempty"`
}

type reportResourceDTO struct {
	Kind   string            `json:"kind" validate:"required,max=64"`
	Name   string            `json:"name" validate:"required,max=1024"`
	Detail map[string]string `json:"detail,omitempty" validate:"max=500"`
}

type reportRequest struct {
	Job       *reportJobDTO       `json:"job,omitempty"`
	Resources []reportResourceDTO `json:"resources,omitempty" validate:"max=5000,dive"`
}

type reportResponse struct {
	OK                bool     `json:"ok"`
	JobAccepted       bool     `json:"job_accepted"`
	ResourcesIngested int      `json:"resources_ingested"`
	Verified          *bool    `json:"verified,omitempty"`
	VerifyFailures    []string `json:"verify_failures,omitempty"`
	SnapshotID        string   `json:"snapshot_id,omitempty"`
}

// --- Grant DTOs ---

type grantObjectDTO struct {
	Key       string `json:"key" validate:"required,max=1024"`
	SizeBytes uint64 `json:"size_bytes"`
}

type uploadGrantsRequest struct {
	Objects []grantObjectDTO `json:"objects" validate:"required,min=1,max=1000,dive"`
}

type uploadGrantsResponse struct {
	Grants []ObjectGrant `json:"grants"`
}

type multipartInitRequest struct {
	Key       string `json:"key" validate:"required,max=1024"`
	SizeBytes uint64 `json:"size_bytes"`
	PartSize  int64  `json:"part_size" validate:"required,min=1048576,max=5368709120"`
}

type multipartInitResponse struct {
	UploadID string   `json:"upload_id"`
	Key      string   `json:"key"`
	PartURLs []string `json:"part_urls"`
}

type multipartPartDTO struct {
	Number int    `json:"number" validate:"min=1,max=10000"`
	ETag   string `json:"etag" validate:"required,max=256"`
	// SHA256/SizeBytes carry the agent-observed per-part integrity claim
	// (bare hex sha256 + exact part length). Shape-checked here; presence
	// is enforced in the handler with an actionable upgrade message.
	SHA256    string `json:"sha256" validate:"max=128"`
	SizeBytes int64  `json:"size_bytes" validate:"gte=0"`
}

type multipartCompleteRequest struct {
	Key      string             `json:"key" validate:"required,max=1024"`
	UploadID string             `json:"upload_id" validate:"required,max=256"`
	Parts    []multipartPartDTO `json:"parts" validate:"required,min=1,max=10000,dive"`
}

type multipartAbortRequest struct {
	Key      string `json:"key" validate:"required,max=1024"`
	UploadID string `json:"upload_id" validate:"required,max=256"`
}

type multipartDoneResponse struct {
	OK  bool   `json:"ok"`
	Key string `json:"key,omitempty"`
}

// --- Restore DTOs ---

type restorePollRequest struct {
	AgentID string `json:"agent_id" validate:"required,max=128"`
}

type restorePollResponse struct {
	Restore *RestoreAssignment `json:"restore"`
}

type downloadGrantsRequest struct {
	Keys []string `json:"keys" validate:"required,min=1,max=10000,dive,max=1024"`
}

type downloadGrantsResponse struct {
	Grants []ObjectGrant `json:"grants"`
}

type restoreReportRequest struct {
	State string `json:"state" validate:"required,max=32"`
	Error string `json:"error" validate:"max=4096"`
}

type restoreReportResponse struct {
	OK bool `json:"ok"`
}

// --- Update DTOs ---

// updateDirective is the poll-advertised update order (omitted when none
// is due). The agent downloads the bytes from update/download and must
// verify sha256 before swapping.
type updateDirective struct {
	Version   string `json:"version"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

type artifactSummary struct {
	ID         string `json:"id"`
	Version    string `json:"version"`
	GOOS       string `json:"goos"`
	GOARCH     string `json:"goarch"`
	SHA256     string `json:"sha256"`
	SizeBytes  int64  `json:"size_bytes"`
	UploadedBy string `json:"uploaded_by"`
	CreatedAt  string `json:"created_at"`
}

type listArtifactsResponse struct {
	Items []artifactSummary `json:"items"`
}

type updateSlotDTO struct {
	TargetVersion string `json:"target_version"`
	GOOS          string `json:"goos"`
	GOARCH        string `json:"goarch"`
	Status        string `json:"status"`
	Error         string `json:"error"`
	RequestedBy   string `json:"requested_by"`
	RequestedAt   string `json:"requested_at"`
	UpdatedAt     string `json:"updated_at"`
}

type describeUpdateResponse struct {
	Latest    *artifactSummary `json:"latest"`
	Available bool             `json:"available"`
	Pending   *updateSlotDTO   `json:"pending"`
}

type requestUpdateRequest struct {
	ArtifactID string `json:"artifact_id" validate:"required,max=128"`
}

type checkUpdatesResponse struct {
	CheckedAt string            `json:"checked_at"`
	Cached    []artifactSummary `json:"cached"`
	UpToDate  bool              `json:"up_to_date"`
}

type batchUpdateRequest struct {
	AgentIDs []string `json:"agent_ids" validate:"required,min=1,max=200,dive,max=128"`
}

type batchUpdateItem struct {
	AgentID       string `json:"agent_id"`
	TargetVersion string `json:"target_version"`
	Status        string `json:"status"`
	Error         string `json:"error"`
}

type batchUpdateResponse struct {
	Results []batchUpdateItem `json:"results"`
}

type updateReportRequest struct {
	Status  string `json:"status" validate:"required,max=32"`
	Version string `json:"version" validate:"required,max=64"`
	Error   string `json:"error" validate:"max=4096"`
}
