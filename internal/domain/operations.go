package domain

import "time"

// NotificationChannel selects the delivery mechanism (PRD §7.16).
type NotificationChannel string

const (
	NotificationEmail   NotificationChannel = "email"
	NotificationWebhook NotificationChannel = "webhook"
)

// NotificationStatus tracks delivery state.
type NotificationStatus string

const (
	NotificationPending NotificationStatus = "pending"
	NotificationSent    NotificationStatus = "sent"
	NotificationFailed  NotificationStatus = "failed"
)

// Notification is an alert about failed jobs or agent state changes.
type Notification struct {
	ID        string
	Channel   NotificationChannel
	Recipient string // address or webhook URL/name; never a secret
	Subject   string
	Body      string
	// Headers carries resolved webhook request headers (rule headers at
	// fanout); other channels ignore it.
	Headers map[string]string
	// RawBody marks Body as a rendered rule template: webhook delivery
	// POSTs it verbatim instead of the default JSON envelope.
	RawBody bool
	Status  NotificationStatus
	// RefType/RefID point at the triggering entity (backup_job, agent...).
	RefType   string
	RefID     string
	Attempts  int
	LastError string
	SentAt    *time.Time
	CreatedAt time.Time
}

// AuditAction names auditable operations (PRD §7.16). Adapters persist
// these; usecases emit them for security-sensitive and administrative acts.
type AuditAction string

const (
	AuditLogin            AuditAction = "login"
	AuditLoginFailed      AuditAction = "login_failed"
	AuditLogout           AuditAction = "logout"
	AuditAgentEnroll      AuditAction = "agent_enroll"
	AuditAgentRevoke      AuditAction = "agent_revoke"
	AuditPolicyChange     AuditAction = "policy_change"
	AuditJobStart         AuditAction = "job_start"
	AuditJobCancel        AuditAction = "job_cancel"
	AuditJobRetry         AuditAction = "job_retry"
	AuditRestoreApprove   AuditAction = "restore_approve"
	AuditRestoreStart     AuditAction = "restore_start"
	AuditStorageChange    AuditAction = "storage_change"
	AuditSecretChange     AuditAction = "secret_change"
	AuditRetentionRun     AuditAction = "retention_run"
	AuditUserChange       AuditAction = "user_change"
	AuditRoleChange       AuditAction = "role_change"
	AuditOIDCChange       AuditAction = "oidc_change"
	AuditOIDCLinkChange   AuditAction = "oidc_link_change"
	AuditNotificationSend AuditAction = "notification_send"
)

// AuditLog records who did what to which target, when, and with what
// outcome. Secrets and backup contents never appear here.
type AuditLog struct {
	ID        string
	ActorID   string // user ID; empty for system/agent actors
	ActorType string // user | agent | system
	Action    AuditAction
	// TargetType/TargetID identify the affected entity.
	TargetType string
	TargetID   string
	Outcome    string // ok | denied | error
	Detail     string // short human-readable context, no secrets
	IPAddress  string
	CreatedAt  time.Time
}

// NewAuditLog builds an audit entry with the creation timestamp set.
func NewAuditLog(now time.Time, actorType, actorID string, action AuditAction, targetType, targetID, outcome, detail string) AuditLog {
	return AuditLog{
		ActorID: actorID, ActorType: actorType,
		Action: action, TargetType: targetType, TargetID: targetID,
		Outcome: outcome, Detail: detail, CreatedAt: now,
	}
}
