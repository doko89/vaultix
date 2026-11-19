package platform

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/vaultix/vaultix/internal/domain"
)

// ---- dashboard DTOs ----

// AgentCounts answers "total/online/offline agents" (PRD §7.15).
type AgentCounts struct {
	Total   int `json:"total"`
	Online  int `json:"online"`
	Offline int `json:"offline"`
	Stale   int `json:"stale"`
	Revoked int `json:"revoked"`
}

// JobSummary is the narrow job slice the dashboard lists.
type JobSummary struct {
	ID         string          `json:"id"`
	AgentID    string          `json:"agent_id"`
	PolicyID   string          `json:"policy_id"`
	State      domain.JobState `json:"state"`
	StartedAt  *string         `json:"started_at,omitempty"`
	FinishedAt *string         `json:"finished_at,omitempty"`
	Error      string          `json:"error,omitempty"`
}

// LastBackup is the latest backup per agent or per policy (PRD §7.15).
type LastBackup struct {
	AgentID      string          `json:"agent_id,omitempty"`
	AgentName    string          `json:"agent_name,omitempty"`
	PolicyID     string          `json:"policy_id,omitempty"`
	PolicyName   string          `json:"policy_name,omitempty"`
	JobID        string          `json:"job_id,omitempty"`
	State        domain.JobState `json:"state,omitempty"`
	FinishedAt   *string         `json:"finished_at,omitempty"`
	HasSucceeded bool            `json:"has_succeeded"`
}

// PolicyGap flags a policy with no recent successful backup (PRD §7.15).
type PolicyGap struct {
	PolicyID      string  `json:"policy_id"`
	PolicyName    string  `json:"policy_name,omitempty"`
	LastSuccessAt *string `json:"last_success_at,omitempty"`
}

// DayPoint is one day of backup volume/duration trend.
type DayPoint struct {
	Date          string `json:"date"`
	Succeeded     int64  `json:"succeeded"`
	Failed        int64  `json:"failed"`
	BytesUploaded uint64 `json:"bytes_uploaded"`
}

// StorageUsage is the storage-consumption rollup, where available.
type StorageUsage struct {
	BytesUsed     uint64 `json:"bytes_used"`
	BytesTotal    uint64 `json:"bytes_total,omitempty"`
	ObjectCount   int64  `json:"object_count,omitempty"`
	SnapshotCount int64  `json:"snapshot_count,omitempty"`
	Available     bool   `json:"available"`
}

// DashboardDTO is the GET /api/dashboard response.
type DashboardDTO struct {
	Agents                 AgentCounts      `json:"agents"`
	JobsByStatus           map[string]int64 `json:"jobs_by_status"`
	RecentJobs             []JobSummary     `json:"recent_jobs"`
	LastBackupPerAgent     []LastBackup     `json:"last_backup_per_agent"`
	LastBackupPerPolicy    []LastBackup     `json:"last_backup_per_policy"`
	PoliciesWithoutSuccess []PolicyGap      `json:"policies_without_recent_success"`
	Trend                  []DayPoint       `json:"trend"`
	Storage                StorageUsage     `json:"storage"`
}

// ---- notification DTOs ----

// RuleDTO is the notification-rule API shape.
type RuleDTO struct {
	ID        string   `json:"id,omitempty"`
	Name      string   `json:"name" validate:"required"`
	Events    []string `json:"events" validate:"required,min=1"`
	Channel   string   `json:"channel" validate:"required,oneof=email webhook"`
	Recipient string   `json:"recipient" validate:"required"`
	Enabled   bool     `json:"enabled"`
}

// ---- audit DTOs ----

// AuditLogDTO is the audit entry API shape.
type AuditLogDTO struct {
	ID         string `json:"id"`
	ActorID    string `json:"actor_id,omitempty"`
	ActorType  string `json:"actor_type"`
	Action     string `json:"action"`
	TargetType string `json:"target_type,omitempty"`
	TargetID   string `json:"target_id,omitempty"`
	Outcome    string `json:"outcome"`
	Detail     string `json:"detail,omitempty"`
	IPAddress  string `json:"ip_address,omitempty"`
	CreatedAt  string `json:"created_at"`
}

// AuditListDTO is the GET /api/audit-logs response.
type AuditListDTO struct {
	Items []AuditLogDTO `json:"items"`
	Total int           `json:"total"`
}

// ---- settings DTOs ----

// GlobalSettings is the global manager configuration (PRD §8 Settings).
type GlobalSettings struct {
	// LocalLoginEnabled keeps the break-glass local admin usable when OIDC
	// is on (PRD §7.1). Disabling requires explicit confirmation (enforced
	// by the UI + auth track, recorded here).
	LocalLoginEnabled bool `json:"local_login_enabled"`
	// DefaultRole applies to users with no mapped role claim; empty denies.
	DefaultRole string `json:"default_role"`
	// SessionLifetimeMinutes / SessionIdleMinutes bound manager sessions
	// (local and OIDC alike).
	SessionLifetimeMinutes int `json:"session_lifetime_minutes"`
	SessionIdleMinutes     int `json:"session_idle_minutes"`
	// HeartbeatTimeoutSeconds ages agents to stale/offline.
	HeartbeatTimeoutSeconds int `json:"heartbeat_timeout_seconds"`
	// GlobalConcurrency caps concurrently dispatched backup jobs (0 = unset).
	GlobalConcurrency int `json:"global_concurrency"`
}

// Validate checks settings invariants.
func (s GlobalSettings) Validate() error {
	switch s.DefaultRole {
	case "", domain.RoleAdministrator, domain.RoleOperator, domain.RoleReadOnly:
	default:
		return errors.New("unknown default role")
	}
	if s.SessionLifetimeMinutes < 0 || s.SessionIdleMinutes < 0 {
		return errors.New("session timeouts must be non-negative")
	}
	if s.SessionIdleMinutes > s.SessionLifetimeMinutes && s.SessionLifetimeMinutes > 0 {
		return errors.New("idle timeout must not exceed session lifetime")
	}
	if s.HeartbeatTimeoutSeconds < 0 || s.GlobalConcurrency < 0 {
		return errors.New("heartbeat timeout and concurrency must be non-negative")
	}
	return nil
}

// DefaultGlobalSettings returns the initial settings.
func DefaultGlobalSettings() GlobalSettings {
	return GlobalSettings{
		LocalLoginEnabled:       true,
		DefaultRole:             "",
		SessionLifetimeMinutes:  12 * 60,
		SessionIdleMinutes:      60,
		HeartbeatTimeoutSeconds: 300,
		GlobalConcurrency:       10,
	}
}

// OIDCProviderInput is the write shape for providers. ClientSecret is
// write-only: create/update accept it, reads never return it (PRD §7.1.1).
type OIDCProviderInput struct {
	Name             string            `json:"name" validate:"required"`
	Issuer           string            `json:"issuer" validate:"required"`
	Enabled          bool              `json:"enabled"`
	DiscoveryURL     string            `json:"discovery_url,omitempty"`
	AuthURL          string            `json:"auth_url,omitempty"`
	TokenURL         string            `json:"token_url,omitempty"`
	JWKSURL          string            `json:"jwks_url,omitempty"`
	UserInfoURL      string            `json:"userinfo_url,omitempty"`
	EndSession       string            `json:"end_session,omitempty"`
	ClientID         string            `json:"client_id" validate:"required"`
	ClientSecret     string            `json:"client_secret,omitempty"`
	Scopes           []string          `json:"scopes,omitempty"`
	GroupClaimNames  []string          `json:"group_claim_names,omitempty"`
	GroupRoleMapping map[string]string `json:"group_role_mapping,omitempty"`
	DefaultRole      string            `json:"default_role,omitempty"`
	JITProvisioning  *bool             `json:"jit_provisioning,omitempty"`
}

// OIDCProviderDTO is the read shape: secret material is replaced by a
// boolean flag (PRD §7.1.1 — secrets never returned by read APIs).
type OIDCProviderDTO struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Issuer           string            `json:"issuer"`
	Enabled          bool              `json:"enabled"`
	DiscoveryURL     string            `json:"discovery_url,omitempty"`
	AuthURL          string            `json:"auth_url,omitempty"`
	TokenURL         string            `json:"token_url,omitempty"`
	JWKSURL          string            `json:"jwks_url,omitempty"`
	UserInfoURL      string            `json:"userinfo_url,omitempty"`
	EndSession       string            `json:"end_session,omitempty"`
	ClientID         string            `json:"client_id"`
	HasClientSecret  bool              `json:"has_client_secret"`
	Scopes           []string          `json:"scopes,omitempty"`
	GroupClaimNames  []string          `json:"group_claim_names,omitempty"`
	GroupRoleMapping map[string]string `json:"group_role_mapping,omitempty"`
	DefaultRole      string            `json:"default_role,omitempty"`
	JITProvisioning  bool              `json:"jit_provisioning"`
	CreatedAt        string            `json:"created_at,omitempty"`
	UpdatedAt        string            `json:"updated_at,omitempty"`
}

// ToDTO converts a provider entity to its secret-free read shape.
func ToDTO(p domain.OIDCProvider) OIDCProviderDTO {
	return OIDCProviderDTO{
		ID: p.ID, Name: p.Name, Issuer: p.Issuer, Enabled: p.Enabled,
		DiscoveryURL: p.DiscoveryURL, AuthURL: p.AuthURL, TokenURL: p.TokenURL,
		JWKSURL: p.JWKSURL, UserInfoURL: p.UserInfoURL, EndSession: p.EndSession,
		ClientID: p.ClientID, HasClientSecret: p.ClientSecretRef != "",
		Scopes: p.Scopes, GroupClaimNames: p.GroupClaimNames,
		GroupRoleMapping: p.GroupRoleMapping, DefaultRole: p.DefaultRole,
		JITProvisioning: p.JITProvisioning,
		CreatedAt:       p.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:       p.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

// ---- shared helpers (slices stay self-contained: no root import) ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": status, "message": message}})
}

// statusFor maps domain errors to HTTP statuses.
func statusFor(err error) int {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, domain.ErrValidation):
		return http.StatusBadRequest
	case errors.Is(err, domain.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, domain.ErrForbidden):
		return http.StatusForbidden
	default:
		return http.StatusInternalServerError
	}
}

func writeDomainError(w http.ResponseWriter, err error) {
	code := statusFor(err)
	msg := "internal server error"
	if code != http.StatusInternalServerError {
		msg = err.Error()
	}
	writeError(w, code, msg)
}

// pathID returns the last path segment (the :id value), or "" when absent.
// Handlers parse IDs from the path directly so they work under any router.
func pathID(r *http.Request, suffix ...string) string {
	p := r.URL.Path
	for _, s := range suffix {
		p = strings.TrimSuffix(p, s)
	}
	p = strings.TrimSuffix(p, "/")
	idx := strings.LastIndex(p, "/")
	if idx < 0 {
		return ""
	}
	return p[idx+1:]
}

// paging parses limit/offset with defaults (25/0, max 100).
func paging(r *http.Request) domain.ListOptions {
	limit := 25
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			limit = n
		}
	}
	if limit > 100 {
		limit = 100
	}
	offset := 0
	if v := strings.TrimSpace(r.URL.Query().Get("offset")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	return domain.ListOptions{Limit: limit, Offset: offset}
}
