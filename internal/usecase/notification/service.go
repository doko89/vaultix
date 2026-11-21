package notification

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// Sender delivers one notification over a single channel.
type Sender interface {
	Channel() domain.NotificationChannel
	Send(ctx context.Context, n *domain.Notification) error
}

// Service evaluates alert events, fans them out to matching rules, persists
// notifications through the domain outbox port, and delivers them through
// registered senders. It implements domain.Notifier.
type Service struct {
	rules   RuleStore
	outbox  domain.NotificationRepository
	senders map[domain.NotificationChannel]Sender
	clock   domain.Clock
	log     *slog.Logger
}

// Deps wires the Service. Rules, Outbox, and at least one Sender are
// required; Clock and Logger default when nil/zero.
type Deps struct {
	Rules   RuleStore
	Outbox  domain.NotificationRepository
	Senders []Sender

	Clock  domain.Clock
	Logger *slog.Logger
}

// New builds a Service.
func New(d Deps) (*Service, error) {
	if d.Rules == nil {
		return nil, fmt.Errorf("notification: RuleStore is required")
	}
	if d.Outbox == nil {
		return nil, fmt.Errorf("notification: NotificationRepository is required")
	}
	if len(d.Senders) == 0 {
		return nil, fmt.Errorf("notification: at least one Sender is required")
	}
	s := &Service{
		rules:   d.Rules,
		outbox:  d.Outbox,
		senders: make(map[domain.NotificationChannel]Sender, len(d.Senders)),
		clock:   d.Clock,
		log:     d.Logger,
	}
	for _, sender := range d.Senders {
		if sender == nil {
			return nil, fmt.Errorf("notification: nil Sender")
		}
		s.senders[sender.Channel()] = sender
	}
	if s.clock == nil {
		s.clock = domain.SystemClock{}
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	return s, nil
}

// Compile-time proof that Service is the domain delivery seam.
var _ domain.Notifier = (*Service)(nil)

// Send implements domain.Notifier: it delivers one notification through the
// sender registered for its channel.
func (s *Service) Send(ctx context.Context, n *domain.Notification) error {
	sender, ok := s.senders[n.Channel]
	if !ok {
		return fmt.Errorf("notification: no sender for channel %q", n.Channel)
	}
	return sender.Send(ctx, n)
}

// NotifyJobFailed fans a failed-job event out to matching rules. Each match
// is persisted to the outbox and delivery is attempted immediately; delivery
// failures mark the row failed (with last_error) rather than failing the
// call, so one bad recipient cannot break job reporting.
//
// INTEGRATION NOTE (jobs track): call this from the job service after a
// backup job reaches JobFailed (and only on the terminal transition, not on
// every progress update).
func (s *Service) NotifyJobFailed(ctx context.Context, ev JobFailedEvent) (int, error) {
	subject := fmt.Sprintf("Vaultix: backup job %s failed", shortID(ev.JobID))
	var b strings.Builder
	fmt.Fprintf(&b, "Backup job %s failed.\n", ev.JobID)
	if ev.PolicyID != "" {
		fmt.Fprintf(&b, "Policy: %s\n", ev.PolicyID)
	}
	if ev.AgentID != "" {
		fmt.Fprintf(&b, "Agent: %s\n", ev.AgentID)
	}
	if ev.Attempt > 0 {
		fmt.Fprintf(&b, "Attempt: %d\n", ev.Attempt)
	}
	if ev.ErrorCode != "" || ev.ErrorMessage != "" {
		fmt.Fprintf(&b, "Error: %s %s\n", ev.ErrorCode, ev.ErrorMessage)
	}
	return s.fanout(ctx, EventJobFailed, "backup_job", ev.JobID, subject, strings.TrimSpace(b.String()))
}

// NotifyAgentTransition fans an agent liveness transition out to matching
// rules. Only meaningful transitions produce events: anything -> offline,
// anything -> stale, and offline/stale -> online (recovery).
//
// INTEGRATION NOTE (agents track): call this from the heartbeat service when
// Agent.Status(now, timeout) changes between two consecutive evaluations.
func (s *Service) NotifyAgentTransition(ctx context.Context, ev AgentTransitionEvent) (int, error) {
	eventType := ""
	subject := ""
	switch {
	case ev.To == domain.AgentStatusOffline && ev.From != domain.AgentStatusOffline:
		eventType = EventAgentOffline
		subject = fmt.Sprintf("Vaultix: agent %s is offline", displayName(ev))
	case ev.To == domain.AgentStatusStale && ev.From != domain.AgentStatusStale:
		eventType = EventAgentStale
		subject = fmt.Sprintf("Vaultix: agent %s heartbeat is stale", displayName(ev))
	case ev.To == domain.AgentStatusOnline &&
		(ev.From == domain.AgentStatusOffline || ev.From == domain.AgentStatusStale):
		eventType = EventAgentOnline
		subject = fmt.Sprintf("Vaultix: agent %s is back online", displayName(ev))
	default:
		return 0, nil // not an alert-worthy transition
	}
	body := fmt.Sprintf("Agent %s transitioned from %s to %s.", displayName(ev), ev.From, ev.To)
	return s.fanout(ctx, eventType, "agent", ev.AgentID, subject, body)
}

func displayName(ev AgentTransitionEvent) string {
	if ev.Hostname != "" {
		return ev.Hostname + " (" + shortID(ev.AgentID) + ")"
	}
	return ev.AgentID
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// fanout persists one notification per matching rule and attempts immediate
// delivery. It returns the number of rules matched.
func (s *Service) fanout(ctx context.Context, eventType, refType, refID, subject, body string) (int, error) {
	rules, err := s.rules.ListRules(ctx)
	if err != nil {
		return 0, fmt.Errorf("notification: list rules: %w", err)
	}
	matched := 0
	for _, r := range rules {
		if !r.Matches(eventType) {
			continue
		}
		matched++
		n := &domain.Notification{
			Channel:   r.Channel,
			Recipient: r.Recipient,
			Subject:   subject,
			Body:      body,
			Status:    domain.NotificationPending,
			RefType:   refType,
			RefID:     refID,
			CreatedAt: s.clock.Now().UTC(),
		}
		if r.Channel == domain.NotificationWebhook {
			n.Headers = cloneHeaders(r.Headers)
			if strings.TrimSpace(r.BodyTemplate) != "" {
				rendered, err := RenderBodyTemplate(r.BodyTemplate, TemplateData{
					Event: eventType, Subject: subject, Body: body,
					Recipient: r.Recipient, RefType: refType, RefID: refID,
					SentAt: n.CreatedAt.Format(time.RFC3339),
				})
				if err != nil {
					s.log.Error("notification template render failed",
						"event", eventType, "rule", r.ID, "error", err)
					continue
				}
				n.Body = rendered
				n.RawBody = true
			}
		}
		if err := s.outbox.CreateNotification(ctx, n); err != nil {
			s.log.Error("notification outbox write failed",
				"event", eventType, "rule", r.ID, "error", err)
			continue
		}
		sender, ok := s.senders[r.Channel]
		if !ok {
			n.Status = domain.NotificationFailed
			n.LastError = fmt.Sprintf("no sender for channel %q", r.Channel)
			_ = s.outbox.UpdateNotification(ctx, n)
			continue
		}
		if err := sender.Send(ctx, n); err != nil {
			n.Status = domain.NotificationFailed
			n.LastError = truncate(err.Error(), 1000)
			n.Attempts++
			_ = s.outbox.UpdateNotification(ctx, n)
			s.log.Warn("notification delivery failed",
				"event", eventType, "rule", r.ID, "channel", r.Channel)
			continue
		}
		now := s.clock.Now().UTC()
		n.Status = domain.NotificationSent
		n.SentAt = &now
		if err := s.outbox.UpdateNotification(ctx, n); err != nil {
			s.log.Error("notification outbox update failed", "error", err)
		}
	}
	return matched, nil
}

// DrainPending redelivers up to limit pending outbox rows. It returns the
// number delivered.
//
// INTEGRATION NOTE (scheduler): wire DrainPending to a River periodic job
// (or a serve-loop ticker until then) so rows that failed immediate delivery
// are retried.
func (s *Service) DrainPending(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 25
	}
	pending, err := s.outbox.ListPending(ctx, limit)
	if err != nil {
		return 0, fmt.Errorf("notification: list pending: %w", err)
	}
	delivered := 0
	for i := range pending {
		n := &pending[i]
		sender, ok := s.senders[n.Channel]
		if !ok {
			continue
		}
		if err := sender.Send(ctx, n); err != nil {
			n.Status = domain.NotificationFailed
			n.LastError = truncate(err.Error(), 1000)
			n.Attempts++
			_ = s.outbox.UpdateNotification(ctx, n)
			continue
		}
		now := s.clock.Now().UTC()
		n.Status = domain.NotificationSent
		n.SentAt = &now
		if err := s.outbox.UpdateNotification(ctx, n); err != nil {
			return delivered, fmt.Errorf("notification: update: %w", err)
		}
		delivered++
	}
	return delivered, nil
}

// ---- Rule administration (backing the /api/notifications API) ----

// CreateRule validates and stores a rule.
func (s *Service) CreateRule(ctx context.Context, r Rule) (Rule, error) {
	created, err := s.rules.CreateRule(ctx, r)
	if err != nil {
		return Rule{}, fmt.Errorf("notification: create rule: %w", err)
	}
	return created, nil
}

// GetRule returns one rule.
func (s *Service) GetRule(ctx context.Context, id string) (Rule, error) {
	r, err := s.rules.GetRule(ctx, id)
	if err != nil {
		return Rule{}, fmt.Errorf("notification: get rule: %w", err)
	}
	return r, nil
}

// ListRules returns all rules.
func (s *Service) ListRules(ctx context.Context) ([]Rule, error) {
	rules, err := s.rules.ListRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("notification: list rules: %w", err)
	}
	return rules, nil
}

// UpdateRule replaces a rule.
func (s *Service) UpdateRule(ctx context.Context, id string, r Rule) (Rule, error) {
	updated, err := s.rules.UpdateRule(ctx, id, r)
	if err != nil {
		return Rule{}, fmt.Errorf("notification: update rule: %w", err)
	}
	return updated, nil
}

// DeleteRule removes a rule.
func (s *Service) DeleteRule(ctx context.Context, id string) error {
	if err := s.rules.DeleteRule(ctx, id); err != nil {
		return fmt.Errorf("notification: delete rule: %w", err)
	}
	return nil
}

// TestRule sends a test message through the rule's channel/recipient without
// persisting a notification row.
func (s *Service) TestRule(ctx context.Context, id string) error {
	r, err := s.rules.GetRule(ctx, id)
	if err != nil {
		return fmt.Errorf("notification: test rule: %w", err)
	}
	sender, ok := s.senders[r.Channel]
	if !ok {
		return fmt.Errorf("notification: no sender for channel %q", r.Channel)
	}
	n := &domain.Notification{
		Channel:   r.Channel,
		Recipient: r.Recipient,
		Subject:   fmt.Sprintf("Vaultix: test notification for rule %q", r.Name),
		Body:      "This is a test notification from Vaultix. The rule is configured correctly.",
		Status:    domain.NotificationPending,
		CreatedAt: s.clock.Now().UTC(),
	}
	if r.Channel == domain.NotificationWebhook {
		n.Headers = cloneHeaders(r.Headers)
		if strings.TrimSpace(r.BodyTemplate) != "" {
			rendered, err := RenderBodyTemplate(r.BodyTemplate, TemplateData{
				Event: "test", Subject: n.Subject, Body: n.Body,
				Recipient: r.Recipient, SentAt: n.CreatedAt.Format(time.RFC3339),
			})
			if err != nil {
				return fmt.Errorf("notification: test rule %q: %w", id, err)
			}
			n.Body = rendered
			n.RawBody = true
		}
	}
	if err := sender.Send(ctx, n); err != nil {
		return fmt.Errorf("notification: test rule %q: %w", id, err)
	}
	return nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func cloneHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
