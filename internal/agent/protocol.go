package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Target types the executor dispatches on. Aliases with dashes or legacy
// short names are normalized by NormalizeTargetType.
const (
	TargetHostFilesystem = "host_filesystem"
	TargetDockerVolume   = "docker_volume"
	TargetPostgres       = "postgres"
	TargetMySQL          = "mysql"
	TargetMSSQL          = "mssql"
	TargetRedis          = "redis"
	TargetStack          = "stack"
)

// NormalizeTargetType maps aliases ("filesystem", "docker-volume", ...) to
// canonical target types. Unknown values pass through unchanged so dispatch
// can report a structured unsupported error.
func NormalizeTargetType(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "filesystem", "host-filesystem", "host_filesystem":
		return TargetHostFilesystem
	case "docker-volume", "docker_volume", "dockervolume":
		return TargetDockerVolume
	case TargetPostgres:
		return TargetPostgres
	case TargetMySQL:
		return TargetMySQL
	case TargetMSSQL:
		return TargetMSSQL
	case TargetRedis:
		return TargetRedis
	case TargetStack:
		return TargetStack
	default:
		return strings.ToLower(strings.TrimSpace(t))
	}
}

// KindToTargetType derives a default target type from a job kind when a
// target carries no explicit type (older manager shape).
func KindToTargetType(kind string) string {
	switch NormalizeTargetType(kind) {
	case TargetHostFilesystem:
		return TargetHostFilesystem
	case TargetDockerVolume:
		return TargetDockerVolume
	case TargetPostgres:
		return TargetPostgres
	case TargetMySQL:
		return TargetMySQL
	default:
		return strings.ToLower(strings.TrimSpace(kind))
	}
}

// EncryptionSpec rides on a polled job. Missing spec (or Enabled=false)
// means unencrypted. KeyB64 carries key material and must never be logged
// or persisted to local state.
type EncryptionSpec struct {
	Enabled bool   `json:"enabled"`
	KeyRef  string `json:"key_ref,omitempty"`
	KeyB64  string `json:"key_b64,omitempty"`
}

// Target is one authorized backup selector inside a job. The executor backs
// up ONLY these selectors — never anything else.
type Target struct {
	Type     string         `json:"type,omitempty"`
	Selector string         `json:"selector"`
	Options  map[string]any `json:"options,omitempty"`
}

// targetAlias decodes the string form ("targets": ["/var/www"]) used by the
// pre-P3 manager shape; each string becomes a selector with empty type,
// resolved later via KindToTargetType.
func (t *Target) UnmarshalJSON(raw []byte) error {
	if string(raw) == "null" {
		return fmt.Errorf("agent: target must not be null")
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		t.Type = ""
		t.Selector = s
		return nil
	}
	type plain Target
	var p plain
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	*t = Target(p)
	return nil
}

// PolledJob is one dispatchable job from GET /api/agent/poll. Unknown kinds
// and target types are kept (not rejected) so dispatch can report a
// structured unsupported error instead of crashing.
type PolledJob struct {
	ID            string         `json:"id"`
	PolicyID      string         `json:"policy_id"`
	PolicyVersion int            `json:"policy_version"`
	Kind          string         `json:"kind"`
	Targets       []Target       `json:"targets"`
	Encryption    EncryptionSpec `json:"encryption"`
	State         string         `json:"state,omitempty"`
	Attempt       int            `json:"attempt,omitempty"`
}

// EncryptionEnabled reports whether this job requires envelope encryption.
// Tolerates a missing key: callers treat enabled-without-key as a job error,
// never as silent plaintext.
func (j PolledJob) EncryptionEnabled() bool { return j.Encryption.Enabled }

// PollResponse is GET /api/agent/poll?limit=.
type PollResponse struct {
	Jobs            []PolledJob `json:"jobs"`
	CancelledJobIDs []string    `json:"cancelled_job_ids"`
	ServerTime      string      `json:"server_time,omitempty"`
}

// UnmarshalJSON tolerates the pre-P3 shape (no cancelled_job_ids, string
// targets, missing encryption) and normalizes nil slices to empty.
func (r *PollResponse) UnmarshalJSON(raw []byte) error {
	type plain PollResponse
	var p plain
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	*r = PollResponse(p)
	if r.Jobs == nil {
		r.Jobs = []PolledJob{}
	}
	if r.CancelledJobIDs == nil {
		r.CancelledJobIDs = []string{}
	}
	return nil
}

// ObjectManifest describes one uploaded object in the completion manifest.
type ObjectManifest struct {
	Key       string `json:"key"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

// CompressionInfo records how backup payloads were compressed.
type CompressionInfo struct {
	Algo  string `json:"algo"`
	Level int    `json:"level"`
}

// ManifestEncryption records envelope encryption without key material.
type ManifestEncryption struct {
	Enabled bool   `json:"enabled"`
	KeyRef  string `json:"key_ref,omitempty"`
}

// Manifest is the completion payload inside a success report.
type Manifest struct {
	Objects         []ObjectManifest   `json:"objects"`
	BytesRead       uint64             `json:"bytes_read"`
	BytesCompressed uint64             `json:"bytes_compressed"`
	BytesUploaded   uint64             `json:"bytes_uploaded"`
	Compression     CompressionInfo    `json:"compression"`
	Encryption      ManifestEncryption `json:"encryption"`
}

// JobReportPayload is POST /api/agent/report's job section. Manifest is set
// only on terminal success; the pre-P3 manager ignores unknown fields.
type JobReportPayload struct {
	JobID         string    `json:"job_id"`
	State         JobState  `json:"state"`
	Error         string    `json:"error,omitempty"`
	BytesRead     *uint64   `json:"bytes_read,omitempty"`
	BytesUploaded *uint64   `json:"bytes_uploaded,omitempty"`
	Manifest      *Manifest `json:"manifest,omitempty"`
}

// --- Upload grants ---

// GrantObject is one object the agent proposes to upload. Key is the
// proposed object name (e.g. "t0-host_filesystem.tar.zst"); the manager
// returns the full storage key verbatim in each grant.
type GrantObject struct {
	Key       string `json:"key"`
	SizeBytes int64  `json:"size_bytes"`
}

// UploadGrantsRequest is POST /api/agent/jobs/{id}/upload-grants.
type UploadGrantsRequest struct {
	Objects []GrantObject `json:"objects"`
}

// UploadGrant carries the manager-issued full key plus its presigned URL.
// Headers, when present, must be sent verbatim on the PUT.
type UploadGrant struct {
	Key     string            `json:"key"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

// UploadGrantsResponse matches request objects by index.
type UploadGrantsResponse struct {
	Grants []UploadGrant `json:"grants"`
}

// --- Multipart upload (objects >100MB) ---

// MultipartInitRequest is POST /api/agent/jobs/{id}/multipart/init.
type MultipartInitRequest struct {
	Key       string `json:"key"`
	SizeBytes int64  `json:"size_bytes"`
	PartSize  int64  `json:"part_size"`
}

// MultipartInitResponse carries one presigned URL per part, in order.
type MultipartInitResponse struct {
	UploadID string   `json:"upload_id"`
	Key      string   `json:"key"`
	PartURLs []string `json:"part_urls"`
}

// MultipartPart identifies one completed part for completion.
type MultipartPart struct {
	Number int    `json:"number"`
	ETag   string `json:"etag"`
}

// MultipartCompleteRequest is POST /api/agent/jobs/{id}/multipart/complete.
type MultipartCompleteRequest struct {
	UploadID string          `json:"upload_id"`
	Key      string          `json:"key"`
	Parts    []MultipartPart `json:"parts"`
}

// MultipartAbortRequest is POST /api/agent/jobs/{id}/multipart/abort.
type MultipartAbortRequest struct {
	UploadID string `json:"upload_id"`
	Key      string `json:"key"`
}

// --- Restores ---

// RestorePollRequest is POST /api/agent/restores/poll.
type RestorePollRequest struct {
	AgentID string `json:"agent_id"`
}

// RestoreDestination says where to materialize the restore.
type RestoreDestination struct {
	Mode string `json:"mode"`
	Path string `json:"path"`
}

// RestoreEncryption carries the data key for envelope decryption. KeyB64
// must never be logged or persisted.
type RestoreEncryption struct {
	Enabled bool   `json:"enabled"`
	KeyRef  string `json:"key_ref,omitempty"`
	KeyB64  string `json:"key_b64,omitempty"`
}

// RestoreObject is one stored object backing the snapshot.
type RestoreObject struct {
	Key       string `json:"key"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

// RestoreJob is one restore dispatched to this agent.
type RestoreJob struct {
	ID              string             `json:"id"`
	SnapshotID      string             `json:"snapshot_id"`
	Kind            string             `json:"kind"`
	Destination     RestoreDestination `json:"destination"`
	OverwritePolicy string             `json:"overwrite_policy"`
	Objects         []RestoreObject    `json:"objects"`
	Encryption      RestoreEncryption  `json:"encryption"`
}

// OverwriteAllowed reports whether existing files may be replaced. Anything
// but an explicit allow refuses: "allow" or "overwrite" (case-insensitive).
func (r RestoreJob) OverwriteAllowed() bool {
	switch strings.ToLower(strings.TrimSpace(r.OverwritePolicy)) {
	case "allow", "overwrite", "true", "yes":
		return true
	default:
		return false
	}
}

// RestorePollResponse carries at most one restore (one job at a time, MVP).
type RestorePollResponse struct {
	Restore *RestoreJob `json:"restore"`
}

// DownloadGrantsRequest is POST /api/agent/restores/{id}/download-grants.
type DownloadGrantsRequest struct {
	Keys []string `json:"keys"`
}

// DownloadGrant pairs a storage key with its presigned GET URL.
type DownloadGrant struct {
	Key string `json:"key"`
	URL string `json:"url"`
}

// DownloadGrantsResponse matches request keys by index.
type DownloadGrantsResponse struct {
	Grants []DownloadGrant `json:"grants"`
}

// RestoreReportRequest is POST /api/agent/restores/{id}/report.
type RestoreReportRequest struct {
	State string `json:"state"`
	Error string `json:"error,omitempty"`
}
