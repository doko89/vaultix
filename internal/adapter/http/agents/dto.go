package agents

import "time"

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
	Jobs       []QueuedJob `json:"jobs"`
	ServerTime time.Time   `json:"server_time"`
}

type reportJobDTO struct {
	JobID         string  `json:"job_id" validate:"required,max=128"`
	State         string  `json:"state" validate:"required,max=32"`
	Error         string  `json:"error" validate:"max=4096"`
	BytesRead     *uint64 `json:"bytes_read,omitempty"`
	BytesUploaded *uint64 `json:"bytes_uploaded,omitempty"`
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
	OK                bool `json:"ok"`
	JobAccepted       bool `json:"job_accepted"`
	ResourcesIngested int  `json:"resources_ingested"`
}
