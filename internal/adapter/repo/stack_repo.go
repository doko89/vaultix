package repo

import (
	"context"

	"github.com/vaultix/vaultix/db/gen"
	"github.com/vaultix/vaultix/internal/domain"
)

// StackRepo implements domain.StackRepository on
// application_stacks/application_stack_resources.
//
// Mapping notes: stack IDs are always database-generated UUIDs.
// ResourceID/Selector are opaque TEXT with no FK to discovered_resources:
// the selector pins the exact backup target when the discovered resource
// no longer exists. Type round-trips as TEXT against the resource_type
// check values (the eight domain target types plus "" for unset).
type StackRepo struct {
	baseRepo
}

// CreateStack inserts a stack. The ID is always database-generated;
// s.ID/CreatedAt/UpdatedAt are populated from the row.
func (r *StackRepo) CreateStack(ctx context.Context, s *domain.ApplicationStack) error {
	if s == nil {
		return validationError("stack is required")
	}
	if err := s.Validate(); err != nil {
		return err
	}
	aid, err := mustParseUUID(s.AgentID, "agent id")
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.CreateStack(ctx, gen.CreateStackParams{
		AgentID:     aid,
		Name:        s.Name,
		Description: s.Description,
		ComposePath: s.ComposePath,
	})
	if err != nil {
		return mapErr(err)
	}
	*s = mapStack(row)
	return nil
}

// GetStack returns one stack by ID.
func (r *StackRepo) GetStack(ctx context.Context, id string) (*domain.ApplicationStack, error) {
	if id == "" {
		return nil, notFound("stack")
	}
	sid, err := mustParseUUID(id, "stack id")
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetStackByID(ctx, gen.GetStackByIDParams{ID: sid})
	if err != nil {
		return nil, mapErr(err)
	}
	out := mapStack(row)
	return &out, nil
}

// UpdateStack rewrites a stack.
func (r *StackRepo) UpdateStack(ctx context.Context, s *domain.ApplicationStack) error {
	if s == nil {
		return validationError("stack is required")
	}
	if err := s.Validate(); err != nil {
		return err
	}
	sid, err := mustParseUUID(s.ID, "stack id")
	if err != nil {
		return err
	}
	aid, err := mustParseUUID(s.AgentID, "agent id")
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.UpdateStack(ctx, gen.UpdateStackParams{
		ID:          sid,
		AgentID:     aid,
		Name:        s.Name,
		Description: s.Description,
		ComposePath: s.ComposePath,
	})
	if err != nil {
		return mapErr(err)
	}
	*s = mapStack(row)
	return nil
}

// DeleteStack removes a stack; members cascade via the stack_id FK.
func (r *StackRepo) DeleteStack(ctx context.Context, id string) error {
	sid, err := mustParseUUID(id, "stack id")
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	n, err := r.q.DeleteStack(ctx, gen.DeleteStackParams{ID: sid})
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return notFound("stack")
	}
	return nil
}

// ListStacks returns an agent's stacks (all stacks when agentID is empty).
func (r *StackRepo) ListStacks(ctx context.Context, agentID string) ([]domain.ApplicationStack, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	if agentID == "" {
		rows, err := r.q.ListStacks(ctx)
		if err != nil {
			return nil, mapErr(err)
		}
		return mapStacks(rows), nil
	}
	aid, err := mustParseUUID(agentID, "agent id")
	if err != nil {
		return nil, err
	}
	rows, err := r.q.ListStacksByAgent(ctx, gen.ListStacksByAgentParams{AgentID: aid})
	if err != nil {
		return nil, mapErr(err)
	}
	return mapStacks(rows), nil
}

// SetStackResources replaces a stack's resource membership.
func (r *StackRepo) SetStackResources(ctx context.Context, stackID string, resources []domain.ApplicationStackResource) error {
	sid, err := mustParseUUID(stackID, "stack id")
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	return mapErr(r.inTx(ctx, func(q *gen.Queries) error {
		if _, err := q.GetStackByID(ctx, gen.GetStackByIDParams{ID: sid}); err != nil {
			return err
		}
		if err := q.DeleteStackResourcesByStack(ctx, gen.DeleteStackResourcesByStackParams{StackID: sid}); err != nil {
			return err
		}
		for _, res := range resources {
			_, err := q.AddStackResource(ctx, gen.AddStackResourceParams{
				StackID:      sid,
				ResourceID:   res.ResourceID,
				Selector:     res.Selector,
				ResourceType: string(res.Type),
				RestoreOrder: saturatingInt32(res.RestoreOrder),
				Required:     res.Required,
			})
			if err != nil {
				return err
			}
		}
		return nil
	}))
}

// ListStackResources returns a stack's resource membership ordered by
// restore order.
func (r *StackRepo) ListStackResources(ctx context.Context, stackID string) ([]domain.ApplicationStackResource, error) {
	sid, err := mustParseUUID(stackID, "stack id")
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	if _, err := r.q.GetStackByID(ctx, gen.GetStackByIDParams{ID: sid}); err != nil {
		return nil, mapErr(err)
	}
	rows, err := r.q.ListStackResources(ctx, gen.ListStackResourcesParams{StackID: sid})
	if err != nil {
		return nil, mapErr(err)
	}
	return mapStackResources(uuidString(sid), rows), nil
}

// mapStack converts a stack row to the domain type.
func mapStack(row gen.ApplicationStack) domain.ApplicationStack {
	return domain.ApplicationStack{
		ID:          uuidString(row.ID),
		AgentID:     uuidString(row.AgentID),
		Name:        row.Name,
		Description: row.Description,
		ComposePath: row.ComposePath,
		CreatedAt:   timeOrZero(row.CreatedAt),
		UpdatedAt:   timeOrZero(row.UpdatedAt),
	}
}

// mapStacks converts stack rows to domain values (never nil).
func mapStacks(rows []gen.ApplicationStack) []domain.ApplicationStack {
	out := make([]domain.ApplicationStack, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapStack(row))
	}
	return out
}

// mapStackResources converts member rows to domain values (never nil).
func mapStackResources(stackID string, rows []gen.ApplicationStackResource) []domain.ApplicationStackResource {
	out := make([]domain.ApplicationStackResource, 0, len(rows))
	for _, row := range rows {
		out = append(out, domain.ApplicationStackResource{
			StackID:      stackID,
			ResourceID:   row.ResourceID,
			Selector:     row.Selector,
			Type:         domain.TargetType(row.ResourceType),
			RestoreOrder: int(row.RestoreOrder),
			Required:     row.Required,
		})
	}
	return out
}
