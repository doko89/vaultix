package repo

import (
	"context"
	"time"

	"github.com/vaultix/vaultix/db/gen"
	"github.com/vaultix/vaultix/internal/domain"
)

// NotificationRepo implements domain.NotificationRepository on the
// notifications outbox. Delivery itself is a Notifier concern; this store
// only persists queue state.
//
// Mapping notes: event_type derives from RefType ("generic" when empty)
// and RefID must be a UUID (or empty) for related_id. scheduled_at always
// defaults to now on enqueue.
type NotificationRepo struct {
	baseRepo
}

// CreateNotification enqueues a pending notification. The ID is
// database-generated.
func (r *NotificationRepo) CreateNotification(ctx context.Context, n *domain.Notification) error {
	if n == nil {
		return validationError("notification is required")
	}
	if n.Channel == "" {
		return validationError("notification channel is required")
	}
	if n.Recipient == "" {
		return validationError("notification recipient is required")
	}
	relatedID, err := mustOptUUID(n.RefID, "ref id")
	if err != nil {
		return err
	}
	eventType := n.RefType
	if eventType == "" {
		eventType = "generic"
	}
	headers, err := encodeJSON(n.Headers)
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.EnqueueNotification(ctx, gen.EnqueueNotificationParams{
		Channel:     string(n.Channel),
		Recipient:   n.Recipient,
		EventType:   eventType,
		Subject:     n.Subject,
		Body:        n.Body,
		RelatedType: textOrNull(n.RefType),
		RelatedID:   relatedID,
		Column8:     nil,
		Headers:     headers,
		BodyRaw:     n.RawBody,
	})
	if err != nil {
		return mapErr(err)
	}
	n.ID = uuidString(row.ID)
	n.Status = domain.NotificationStatus(row.State)
	n.Attempts = int(row.Attempts)
	n.CreatedAt = timeOrZero(row.CreatedAt)
	return nil
}

// UpdateNotification rewrites queue state (delivery outcomes). Marking sent
// without a timestamp stamps now.
func (r *NotificationRepo) UpdateNotification(ctx context.Context, n *domain.Notification) error {
	if n == nil {
		return validationError("notification is required")
	}
	nid, err := mustParseUUID(n.ID, "notification id")
	if err != nil {
		return err
	}
	if n.Status == domain.NotificationSent && n.SentAt == nil {
		now := time.Now()
		n.SentAt = &now
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	if _, err := r.q.GetNotification(ctx, gen.GetNotificationParams{ID: nid}); err != nil {
		return mapErr(err)
	}
	err = r.q.UpdateNotification(ctx, gen.UpdateNotificationParams{
		ID:        nid,
		Recipient: n.Recipient,
		Subject:   n.Subject,
		Body:      n.Body,
		State:     string(n.Status),
		Attempts:  int32OrZero(n.Attempts),
		LastError: textOrNull(n.LastError),
		SentAt:    timestamptzPtr(n.SentAt),
	})
	return mapErr(err)
}

// ListPending returns due pending notifications, oldest first.
func (r *NotificationRepo) ListPending(ctx context.Context, limit int) ([]domain.Notification, error) {
	if limit <= 0 {
		limit = defaultPageLimit
	}
	if limit > maxPageLimit {
		limit = maxPageLimit
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	rows, err := r.q.ListPendingNotifications(ctx, gen.ListPendingNotificationsParams{Limit: int32OrZero(limit)})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]domain.Notification, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapNotification(row))
	}
	return out, nil
}

func mapNotification(row gen.Notification) domain.Notification {
	var headers map[string]string
	_ = decodeJSON(row.Headers, &headers) // written by us; corrupt rows read empty
	return domain.Notification{
		ID:        uuidString(row.ID),
		Channel:   domain.NotificationChannel(row.Channel),
		Recipient: row.Recipient,
		Subject:   row.Subject,
		Body:      row.Body,
		Headers:   headers,
		RawBody:   row.BodyRaw,
		Status:    domain.NotificationStatus(row.State),
		RefType:   textOrEmpty(row.RelatedType),
		RefID:     uuidString(row.RelatedID),
		Attempts:  int(row.Attempts),
		LastError: textOrEmpty(row.LastError),
		SentAt:    timePtr(row.SentAt),
		CreatedAt: timeOrZero(row.CreatedAt),
	}
}
