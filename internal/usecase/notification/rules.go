package notification

import (
	"context"
	"fmt"
	"strings"

	"github.com/vaultix/vaultix/internal/domain"
)

// Event types fanned out to rules (PRD §7.16: failed jobs and agent state
// changes). Stored on domain.Notification via RefType/RefID + subject.
const (
	EventJobFailed    = "job_failed"
	EventAgentOffline = "agent_offline"
	EventAgentStale   = "agent_stale"
	EventAgentOnline  = "agent_online"
)

// JobFailedEvent describes one failed backup job execution.
type JobFailedEvent struct {
	JobID        string
	AgentID      string
	PolicyID     string
	ErrorCode    string
	ErrorMessage string
	Attempt      int
}

// AgentTransitionEvent describes one agent liveness transition. From/To use
// domain.AgentStatus; the evaluator maps the pair onto an event type.
type AgentTransitionEvent struct {
	AgentID  string
	Hostname string
	From     domain.AgentStatus
	To       domain.AgentStatus
}

// Rule routes one or more event types to a channel recipient. Recipients
// are email addresses or webhook targets: a full http(s) URL posts
// directly, otherwise the name resolves server-side from configuration
// (see WebhookSender).
type Rule struct {
	ID      string
	Name    string
	Events  []string
	Channel domain.NotificationChannel
	// Recipient is an email address or webhook URL/endpoint name.
	Recipient string
	// Headers adds webhook request headers (webhook channel only).
	Headers map[string]string
	// BodyTemplate overrides the webhook POST body, rendered as a Go
	// template over TemplateData (empty keeps the default JSON shape).
	BodyTemplate string
	Enabled      bool
}

// Validate checks rule invariants.
func (r Rule) Validate() error {
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("%w: rule name is required", domain.ErrValidation)
	}
	if len(r.Events) == 0 {
		return fmt.Errorf("%w: rule needs at least one event type", domain.ErrValidation)
	}
	for _, e := range r.Events {
		switch e {
		case EventJobFailed, EventAgentOffline, EventAgentStale, EventAgentOnline:
		default:
			return fmt.Errorf("%w: unknown event type %q", domain.ErrValidation, e)
		}
	}
	switch r.Channel {
	case domain.NotificationEmail, domain.NotificationWebhook:
	default:
		return fmt.Errorf("%w: unknown channel %q", domain.ErrValidation, r.Channel)
	}
	if strings.TrimSpace(r.Recipient) == "" {
		return fmt.Errorf("%w: recipient is required", domain.ErrValidation)
	}
	if r.Channel == domain.NotificationEmail && !strings.Contains(r.Recipient, "@") {
		return fmt.Errorf("%w: email recipient must be an address", domain.ErrValidation)
	}
	if len(r.Headers) > maxRuleHeaders {
		return fmt.Errorf("%w: at most %d headers", domain.ErrValidation, maxRuleHeaders)
	}
	for k, v := range r.Headers {
		if strings.TrimSpace(k) == "" {
			return fmt.Errorf("%w: header names must not be blank", domain.ErrValidation)
		}
		if strings.ContainsAny(v, "\r\n") {
			return fmt.Errorf("%w: header %q must not contain newlines", domain.ErrValidation, k)
		}
	}
	if len(r.BodyTemplate) > maxBodyTemplateBytes {
		return fmt.Errorf("%w: body template exceeds %d bytes", domain.ErrValidation, maxBodyTemplateBytes)
	}
	if strings.TrimSpace(r.BodyTemplate) != "" {
		if err := CheckBodyTemplate(r.BodyTemplate); err != nil {
			return err
		}
	}
	return nil
}

// Matches reports whether the rule fires for the event type.
func (r Rule) Matches(eventType string) bool {
	if !r.Enabled {
		return false
	}
	for _, e := range r.Events {
		if e == eventType {
			return true
		}
	}
	return false
}

// RuleStore persists notification rules.
//
// INTEGRATION NOTE (repos track): no notification_rules table exists in
// db/migrations (repos track owns schema + sqlc with NO new migrations this
// phase), so production wiring starts with StaticRuleStore built from manager
// config. If the repos track adds a rules table later, it implements this
// interface and serve.go swaps the constructor — Service code is unchanged.
type RuleStore interface {
	CreateRule(ctx context.Context, r Rule) (Rule, error)
	GetRule(ctx context.Context, id string) (Rule, error)
	ListRules(ctx context.Context) ([]Rule, error)
	UpdateRule(ctx context.Context, id string, r Rule) (Rule, error)
	DeleteRule(ctx context.Context, id string) error
}
