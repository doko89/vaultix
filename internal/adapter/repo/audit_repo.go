package repo

import (
	"context"

	"github.com/vaultix/vaultix/db/gen"
	"github.com/vaultix/vaultix/internal/domain"
)

// AuditRepo implements domain.AuditRepository on audit_logs. The log is
// append-only by design: there are no update or delete operations, and no
// sqlc queries exist for them.
//
// Mapping notes: outcome "ok" maps to the schema "success" value (denied/
// error pass through). Detail rides in the details JSON object; actor_label
// stays empty (domain has none). An empty ActorType defaults to "user" so
// the actor check passes.
type AuditRepo struct {
	baseRepo
}

// auditDetails is the audit_logs.details JSON codec.
type auditDetails struct {
	Detail string `json:"detail,omitempty"`
}

// Append records one audit event. Secrets and backup contents must never
// appear in Detail; the repository cannot verify that, so callers must.
func (r *AuditRepo) Append(ctx context.Context, e *domain.AuditLog) error {
	if e == nil {
		return validationError("audit log is required")
	}
	if e.Action == "" {
		return validationError("audit action is required")
	}
	actorID, err := mustOptUUID(e.ActorID, "actor id")
	if err != nil {
		return err
	}
	outcome, err := auditOutcomeToDB(e.Outcome)
	if err != nil {
		return err
	}
	actorType := e.ActorType
	if actorType == "" {
		actorType = "user"
	}
	details, err := encodeJSON(auditDetails{Detail: e.Detail})
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.CreateAuditEvent(ctx, gen.CreateAuditEventParams{
		ActorID:    actorID,
		ActorType:  actorType,
		ActorLabel: "",
		Action:     string(e.Action),
		TargetType: textOrNull(e.TargetType),
		TargetID:   textOrNull(e.TargetID),
		Outcome:    outcome,
		IpAddress:  e.IPAddress,
		Details:    details,
	})
	if err != nil {
		return mapErr(err)
	}
	e.ID = uuidString(row.ID)
	e.CreatedAt = timeOrZero(row.CreatedAt)
	return nil
}

// List returns one page of audit events narrowed by the filter.
func (r *AuditRepo) List(ctx context.Context, opts domain.ListOptions, filter domain.AuditFilter) (domain.ListResult[domain.AuditLog], error) {
	actorID, err := mustOptUUID(filter.ActorID, "actor id")
	if err != nil {
		return domain.ListResult[domain.AuditLog]{}, err
	}
	limit, offset := page(opts)
	params := gen.ListAuditEventsFilteredParams{
		Column1: actorID,
		Column2: string(filter.Action),
		Column3: timestamptzPtr(filter.Since),
		Column4: timestamptzPtr(filter.Until),
		Limit:   limit,
		Offset:  offset,
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	rows, err := r.q.ListAuditEventsFiltered(ctx, params)
	if err != nil {
		return domain.ListResult[domain.AuditLog]{}, mapErr(err)
	}
	total, err := r.q.CountAuditEventsFiltered(ctx, gen.CountAuditEventsFilteredParams{
		Column1: params.Column1,
		Column2: params.Column2,
		Column3: params.Column3,
		Column4: params.Column4,
	})
	if err != nil {
		return domain.ListResult[domain.AuditLog]{}, mapErr(err)
	}
	out := make([]domain.AuditLog, 0, len(rows))
	for _, row := range rows {
		entry, err := mapAuditLog(row)
		if err != nil {
			return domain.ListResult[domain.AuditLog]{}, err
		}
		out = append(out, entry)
	}
	return domain.ListResult[domain.AuditLog]{Items: out, Total: int(total)}, nil
}

// auditOutcomeToDB maps domain outcomes onto the schema check values.
func auditOutcomeToDB(outcome string) (string, error) {
	switch outcome {
	case "", "ok", "success":
		return "success", nil
	case "denied":
		return "denied", nil
	case "error":
		return "error", nil
	default:
		return "", validationError("unknown audit outcome")
	}
}

func mapAuditLog(row gen.AuditLog) (domain.AuditLog, error) {
	var details auditDetails
	if err := decodeJSON(row.Details, &details); err != nil {
		return domain.AuditLog{}, err
	}
	outcome := row.Outcome
	if outcome == "success" {
		outcome = "ok"
	}
	return domain.AuditLog{
		ID:         uuidString(row.ID),
		ActorID:    uuidString(row.ActorID),
		ActorType:  row.ActorType,
		Action:     domain.AuditAction(row.Action),
		TargetType: textOrEmpty(row.TargetType),
		TargetID:   textOrEmpty(row.TargetID),
		Outcome:    outcome,
		Detail:     details.Detail,
		IPAddress:  row.IpAddress,
		CreatedAt:  timeOrZero(row.CreatedAt),
	}, nil
}
