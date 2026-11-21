package repo

import (
	"context"

	"github.com/vaultix/vaultix/db/gen"
	"github.com/vaultix/vaultix/internal/domain"
	notifuc "github.com/vaultix/vaultix/internal/usecase/notification"
)

// RuleRepo implements notification RuleStore on notification_rules. It
// replaces the in-memory StaticRuleStore in production wiring (see the
// RuleStore integration note): rules survive restarts.
type RuleRepo struct {
	baseRepo
}

func mapRule(row gen.NotificationRule) notifuc.Rule {
	var headers map[string]string
	_ = decodeJSON(row.Headers, &headers) // written by us; corrupt rows read empty
	return notifuc.Rule{
		ID:           uuidString(row.ID),
		Name:         row.Name,
		Events:       append([]string(nil), row.Events...),
		Channel:      domain.NotificationChannel(row.Channel),
		Recipient:    row.Recipient,
		Headers:      headers,
		BodyTemplate: row.BodyTemplate,
		Enabled:      row.Enabled,
	}
}

// CreateRule validates and persists one rule.
func (r *RuleRepo) CreateRule(ctx context.Context, rule notifuc.Rule) (notifuc.Rule, error) {
	if err := rule.Validate(); err != nil {
		return notifuc.Rule{}, err
	}
	headers, err := encodeJSON(rule.Headers)
	if err != nil {
		return notifuc.Rule{}, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.CreateNotificationRule(ctx, gen.CreateNotificationRuleParams{
		Name:         rule.Name,
		Events:       rule.Events,
		Channel:      string(rule.Channel),
		Recipient:    rule.Recipient,
		Headers:      headers,
		BodyTemplate: rule.BodyTemplate,
		Enabled:      rule.Enabled,
	})
	if err != nil {
		return notifuc.Rule{}, mapErr(err)
	}
	return mapRule(row), nil
}

// GetRule returns one rule by ID.
func (r *RuleRepo) GetRule(ctx context.Context, id string) (notifuc.Rule, error) {
	aid, err := mustParseUUID(id, "rule id")
	if err != nil {
		return notifuc.Rule{}, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetNotificationRule(ctx, gen.GetNotificationRuleParams{ID: aid})
	if err != nil {
		return notifuc.Rule{}, mapErr(err)
	}
	return mapRule(row), nil
}

// ListRules returns all rules, oldest first.
func (r *RuleRepo) ListRules(ctx context.Context) ([]notifuc.Rule, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	rows, err := r.q.ListNotificationRules(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]notifuc.Rule, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapRule(row))
	}
	return out, nil
}

// UpdateRule validates and replaces one rule.
func (r *RuleRepo) UpdateRule(ctx context.Context, id string, rule notifuc.Rule) (notifuc.Rule, error) {
	rule.ID = id
	if err := rule.Validate(); err != nil {
		return notifuc.Rule{}, err
	}
	aid, err := mustParseUUID(id, "rule id")
	if err != nil {
		return notifuc.Rule{}, err
	}
	headers, err := encodeJSON(rule.Headers)
	if err != nil {
		return notifuc.Rule{}, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.UpdateNotificationRule(ctx, gen.UpdateNotificationRuleParams{
		ID:           aid,
		Name:         rule.Name,
		Events:       rule.Events,
		Channel:      string(rule.Channel),
		Recipient:    rule.Recipient,
		Headers:      headers,
		BodyTemplate: rule.BodyTemplate,
		Enabled:      rule.Enabled,
	})
	if err != nil {
		return notifuc.Rule{}, mapErr(err)
	}
	return mapRule(row), nil
}

// DeleteRule removes one rule.
func (r *RuleRepo) DeleteRule(ctx context.Context, id string) error {
	aid, err := mustParseUUID(id, "rule id")
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	n, err := r.q.DeleteNotificationRule(ctx, gen.DeleteNotificationRuleParams{ID: aid})
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return notFound("notification rule")
	}
	return nil
}
