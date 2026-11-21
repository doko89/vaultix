package domain

import (
	"context"
	"time"
)

// Ports are the seams usecases program against; adapters implement them.
// Each repository port is per-aggregate; external-service ports (object
// storage, OIDC, notifier, clock) isolate I/O for testing and swapping
// implementations. No package outside cmd imports an adapter directly.

// ListOptions paginates and filters list queries. Adapters apply sane
// defaults when Limit <= 0.
type ListOptions struct {
	Limit  int
	Offset int
	// Query is a free-text filter interpreted per repository.
	Query string
}

// ListResult carries one page plus the total match count.
type ListResult[T any] struct {
	Items []T
	Total int
}

// ---- Identity aggregate ----

// UserRepository persists users, roles, and memberships.
type UserRepository interface {
	CreateUser(ctx context.Context, u *User) error
	GetUser(ctx context.Context, id string) (*User, error)
	GetUserByUsername(ctx context.Context, username string) (*User, error)
	UpdateUser(ctx context.Context, u *User) error
	ListUsers(ctx context.Context, opts ListOptions) (ListResult[User], error)

	GetRole(ctx context.Context, id string) (*Role, error)
	GetRoleByName(ctx context.Context, name string) (*Role, error)
	ListRoles(ctx context.Context) ([]Role, error)
	ListUserRoles(ctx context.Context, userID string) ([]Role, error)
	GrantRole(ctx context.Context, userID, roleID, grantedBy string) error
	RevokeRole(ctx context.Context, userID, roleID string) error
	// ReplaceUserRoles sets the exact role set (used by OIDC claim refresh).
	ReplaceUserRoles(ctx context.Context, userID string, roleIDs []string) error
}

// OIDCProviderRepository persists OIDC provider configs and identity links.
type OIDCProviderRepository interface {
	CreateProvider(ctx context.Context, p *OIDCProvider) error
	GetProvider(ctx context.Context, id string) (*OIDCProvider, error)
	GetProviderByIssuer(ctx context.Context, issuer string) (*OIDCProvider, error)
	UpdateProvider(ctx context.Context, p *OIDCProvider) error
	ListProviders(ctx context.Context) ([]OIDCProvider, error)

	LinkIdentity(ctx context.Context, i *UserExternalIdentity) error
	GetIdentity(ctx context.Context, issuer, subject string) (*UserExternalIdentity, error)
	ListUserIdentities(ctx context.Context, userID string) ([]UserExternalIdentity, error)
	UpdateIdentity(ctx context.Context, i *UserExternalIdentity) error
	UnlinkIdentity(ctx context.Context, id string) error
}

// ---- Agent aggregate ----

// AgentRepository persists agents, credentials, and heartbeats.
type AgentRepository interface {
	CreateAgent(ctx context.Context, a *Agent) error
	GetAgent(ctx context.Context, id string) (*Agent, error)
	UpdateAgent(ctx context.Context, a *Agent) error
	DeleteAgent(ctx context.Context, id string) error
	ListAgents(ctx context.Context, opts ListOptions) (ListResult[Agent], error)

	StoreCredential(ctx context.Context, c *AgentCredential) error
	GetCredentialByHash(ctx context.Context, hash string) (*AgentCredential, error)
	ListAgentCredentials(ctx context.Context, agentID string) ([]AgentCredential, error)
	UpdateCredential(ctx context.Context, c *AgentCredential) error

	RecordHeartbeat(ctx context.Context, h *AgentHeartbeat) error
	LatestHeartbeat(ctx context.Context, agentID string) (*AgentHeartbeat, error)
}

// DiscoveryRepository persists discovered resources.
type DiscoveryRepository interface {
	// ReplaceAgentResources swaps the agent's inventory snapshot.
	ReplaceAgentResources(ctx context.Context, agentID string, resources []DiscoveredResource) error
	ListAgentResources(ctx context.Context, agentID string, kind ResourceKind) ([]DiscoveredResource, error)
	GetResource(ctx context.Context, id string) (*DiscoveredResource, error)
}

// ---- Policy aggregate ----

// StorageRepository persists storage destinations and secret profiles.
type StorageRepository interface {
	CreateDestination(ctx context.Context, d *StorageDestination) error
	GetDestination(ctx context.Context, id string) (*StorageDestination, error)
	GetDefaultDestination(ctx context.Context) (*StorageDestination, error)
	UpdateDestination(ctx context.Context, d *StorageDestination) error
	DeleteDestination(ctx context.Context, id string) error
	ListDestinations(ctx context.Context) ([]StorageDestination, error)

	CreateSecret(ctx context.Context, s *SecretProfile) error
	GetSecret(ctx context.Context, id string) (*SecretProfile, error)
	DeleteSecret(ctx context.Context, id string) error
	ListSecrets(ctx context.Context) ([]SecretProfile, error)
}

// PolicyRepository persists policies and immutable versions.
type PolicyRepository interface {
	CreatePolicy(ctx context.Context, p *BackupPolicy) error
	GetPolicy(ctx context.Context, id string) (*BackupPolicy, error)
	UpdatePolicy(ctx context.Context, p *BackupPolicy) error
	DeletePolicy(ctx context.Context, id string) error
	ListPolicies(ctx context.Context, opts ListOptions) (ListResult[BackupPolicy], error)

	PublishVersion(ctx context.Context, v *BackupPolicyVersion) error
	GetVersion(ctx context.Context, policyID string, version int) (*BackupPolicyVersion, error)
	ListVersions(ctx context.Context, policyID string) ([]BackupPolicyVersion, error)
}

// ScheduleRepository persists schedules.
type ScheduleRepository interface {
	CreateSchedule(ctx context.Context, s *Schedule) error
	GetSchedule(ctx context.Context, id string) (*Schedule, error)
	UpdateSchedule(ctx context.Context, s *Schedule) error
	DeleteSchedule(ctx context.Context, id string) error
	ListSchedules(ctx context.Context, policyID string) ([]Schedule, error)
	// DueSchedules returns active schedules with NextRunAt <= now, for the
	// scheduler worker to claim.
	DueSchedules(ctx context.Context, now time.Time, limit int) ([]Schedule, error)
}

// ---- Execution aggregate ----

// BackupJobRepository persists backup jobs.
type BackupJobRepository interface {
	CreateJob(ctx context.Context, j *BackupJob) error
	GetJob(ctx context.Context, id string) (*BackupJob, error)
	UpdateJob(ctx context.Context, j *BackupJob) error
	ListJobs(ctx context.Context, opts ListOptions, filter JobFilter) (ListResult[BackupJob], error)
	// ClaimNextJob atomically assigns a queued job to an agent.
	ClaimNextJob(ctx context.Context, agentID string) (*BackupJob, error)
}

// JobFilter narrows job listings.
type JobFilter struct {
	AgentID  string
	PolicyID string
	State    JobState
}

// RestoreJobRepository persists restore jobs.
type RestoreJobRepository interface {
	CreateJob(ctx context.Context, j *RestoreJob) error
	GetJob(ctx context.Context, id string) (*RestoreJob, error)
	UpdateJob(ctx context.Context, j *RestoreJob) error
	ListJobs(ctx context.Context, opts ListOptions, agentID string) (ListResult[RestoreJob], error)
}

// ---- Catalog aggregate ----

// SnapshotRepository persists snapshots, manifests, and objects.
type SnapshotRepository interface {
	CreateSnapshot(ctx context.Context, s *Snapshot) error
	GetSnapshot(ctx context.Context, id string) (*Snapshot, error)
	UpdateSnapshot(ctx context.Context, s *Snapshot) error
	DeleteSnapshot(ctx context.Context, id string) error
	ListSnapshots(ctx context.Context, opts ListOptions, filter SnapshotFilter) (ListResult[Snapshot], error)

	PutManifest(ctx context.Context, m *SnapshotManifest) error
	GetManifest(ctx context.Context, snapshotID string) (*SnapshotManifest, error)
	PutObjects(ctx context.Context, objects []SnapshotObject) error
	ListObjects(ctx context.Context, manifestID string) ([]SnapshotObject, error)
}

// SnapshotFilter narrows catalog browsing (agent, policy, target, date).
type SnapshotFilter struct {
	AgentID    string
	PolicyID   string
	TargetType TargetType
	Since      *time.Time
	Until      *time.Time
	Restorable bool // only verified snapshots with manifests
}

// StackRepository persists application stacks (MVP: minimal).
type StackRepository interface {
	CreateStack(ctx context.Context, s *ApplicationStack) error
	GetStack(ctx context.Context, id string) (*ApplicationStack, error)
	UpdateStack(ctx context.Context, s *ApplicationStack) error
	DeleteStack(ctx context.Context, id string) error
	ListStacks(ctx context.Context, agentID string) ([]ApplicationStack, error)
	SetStackResources(ctx context.Context, stackID string, resources []ApplicationStackResource) error
	ListStackResources(ctx context.Context, stackID string) ([]ApplicationStackResource, error)
}

// ---- Operations aggregate ----

// NotificationRepository persists notifications; Notifier delivers them.
type NotificationRepository interface {
	CreateNotification(ctx context.Context, n *Notification) error
	UpdateNotification(ctx context.Context, n *Notification) error
	ListPending(ctx context.Context, limit int) ([]Notification, error)
}

// AuditRepository persists audit logs (append-only).
type AuditRepository interface {
	Append(ctx context.Context, e *AuditLog) error
	List(ctx context.Context, opts ListOptions, filter AuditFilter) (ListResult[AuditLog], error)
}

// AuditFilter narrows audit listing.
type AuditFilter struct {
	ActorID string
	Action  AuditAction
	Since   *time.Time
	Until   *time.Time
}

// ---- External-service ports ----

// ObjectStorage issues scoped access for the data plane. Agents upload and
// download directly; the Manager only mints short-lived grants and verifies
// integrity metadata.
type ObjectStorage interface {
	// PresignUpload returns a short-lived URL for one object write.
	PresignUpload(ctx context.Context, destinationID, objectKey string, ttl time.Duration) (string, error)
	// PresignDownload returns a short-lived URL for one object read.
	PresignDownload(ctx context.Context, destinationID, objectKey string, ttl time.Duration) (string, error)
	// Head returns stored size/checksum for verification.
	Head(ctx context.Context, destinationID, objectKey string) (ObjectMeta, error)
	// Delete removes one object (retention pruning).
	Delete(ctx context.Context, destinationID, objectKey string) error
	// Ping tests destination connectivity and credentials.
	Ping(ctx context.Context, destinationID string) error
}

// ObjectMeta is stored-object integrity metadata.
type ObjectMeta struct {
	Size     uint64
	Checksum string // algorithm:hex
	Exists   bool
}

// OIDCClient is the Relying-Party seam for interactive login.
type OIDCClient interface {
	// AuthURL builds the authorization URL (code + PKCE, state, nonce).
	AuthURL(provider *OIDCProvider, state, nonce, codeChallenge, redirectURI string) (string, error)
	// Exchange validates the callback and returns verified claims.
	Exchange(ctx context.Context, provider *OIDCProvider, code, codeVerifier, redirectURI, expectedNonce string) (*OIDCClaims, error)
	// LogoutURL builds the RP-initiated logout URL, or "" when the
	// provider publishes no end_session_endpoint.
	LogoutURL(provider *OIDCProvider, postLogoutRedirect string) string
}

// OIDCClaims are the verified ID-token claims needed for provisioning.
type OIDCClaims struct {
	Subject string
	Issuer  string
	Email   string
	Name    string
	Groups  []string
	Expiry  time.Time
}

// Notifier delivers notifications over email/webhook transports.
type Notifier interface {
	Send(ctx context.Context, n *Notification) error
}

// Clock abstracts time for scheduler and expiry logic in tests.
type Clock interface {
	Now() time.Time
}

// SystemClock is the production Clock.
type SystemClock struct{}

// Now returns the current time.
func (SystemClock) Now() time.Time { return time.Now() }
