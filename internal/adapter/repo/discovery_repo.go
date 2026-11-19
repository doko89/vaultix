package repo

import (
	"context"
	"math"

	"github.com/vaultix/vaultix/db/gen"
	"github.com/vaultix/vaultix/internal/domain"
)

// DiscoveryRepo implements domain.DiscoveryRepository on
// discovered_resources.
//
// Mapping notes: the schema resource_type check allows only six values, so
// domain kinds collapse (all database kinds to "database", mount to
// "host_path"). The original kind round-trips in the details JSON under the
// reserved "_kind" key (a caller-supplied "_kind" entry is overwritten).
// Name feeds both resource_key and display_name.
type DiscoveryRepo struct {
	baseRepo
}

// discoveryKindKey reserves the details entry carrying the domain kind.
const discoveryKindKey = "_kind"

// ReplaceAgentResources swaps the agent's inventory snapshot: upserts every
// input row (preserving first-seen timestamps) and deletes stored rows
// absent from the input, atomically.
func (r *DiscoveryRepo) ReplaceAgentResources(ctx context.Context, agentID string, resources []domain.DiscoveredResource) error {
	aid, err := mustParseUUID(agentID, "agent id")
	if err != nil {
		return err
	}
	for i := range resources {
		resources[i].AgentID = agentID
		if err := resources[i].Validate(); err != nil {
			return err
		}
	}
	encoded := make([]gen.UpsertDiscoveredResourceParams, 0, len(resources))
	keep := make(map[[2]string]struct{}, len(resources))
	for _, res := range resources {
		dbType := discoveryTypeToDB(res.Kind)
		details, err := encodeDiscoveryDetails(res.Kind, res.Detail)
		if err != nil {
			return err
		}
		encoded = append(encoded, gen.UpsertDiscoveredResourceParams{
			AgentID:      aid,
			ResourceType: dbType,
			ResourceKey:  res.Name,
			DisplayName:  res.Name,
			Details:      details,
		})
		keep[[2]string{dbType, res.Name}] = struct{}{}
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	return mapErr(r.inTx(ctx, func(q *gen.Queries) error {
		existing, err := q.ListResourcesByAgent(ctx, gen.ListResourcesByAgentParams{
			AgentID: aid,
			Limit:   math.MaxInt32,
			Offset:  0,
		})
		if err != nil {
			return err
		}
		for _, params := range encoded {
			if _, err := q.UpsertDiscoveredResource(ctx, params); err != nil {
				return err
			}
		}
		for _, row := range existing {
			if _, ok := keep[[2]string{row.ResourceType, row.ResourceKey}]; !ok {
				if err := q.DeleteDiscoveredResource(ctx, gen.DeleteDiscoveredResourceParams{ID: row.ID}); err != nil {
					return err
				}
			}
		}
		return nil
	}))
}

// ListAgentResources returns an agent's inventory, optionally filtered by
// kind. Database-kind filters post-filter in Go because several domain
// kinds share the "database" storage type.
func (r *DiscoveryRepo) ListAgentResources(ctx context.Context, agentID string, kind domain.ResourceKind) ([]domain.DiscoveredResource, error) {
	aid, err := mustParseUUID(agentID, "agent id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	var rows []gen.DiscoveredResource
	if kind == "" {
		rows, err = r.q.ListResourcesByAgent(ctx, gen.ListResourcesByAgentParams{
			AgentID: aid,
			Limit:   math.MaxInt32,
			Offset:  0,
		})
	} else {
		rows, err = r.q.ListResourcesByAgentAndType(ctx, gen.ListResourcesByAgentAndTypeParams{
			AgentID:      aid,
			ResourceType: discoveryTypeToDB(kind),
			Limit:        math.MaxInt32,
			Offset:       0,
		})
	}
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]domain.DiscoveredResource, 0, len(rows))
	for _, row := range rows {
		res, err := mapDiscoveredResource(row)
		if err != nil {
			return nil, err
		}
		if kind != "" && res.Kind != kind {
			continue
		}
		out = append(out, res)
	}
	return out, nil
}

// GetResource returns one discovered resource by ID.
func (r *DiscoveryRepo) GetResource(ctx context.Context, id string) (*domain.DiscoveredResource, error) {
	rid, err := mustParseUUID(id, "resource id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetDiscoveredResource(ctx, gen.GetDiscoveredResourceParams{ID: rid})
	if err != nil {
		return nil, mapErr(err)
	}
	res, err := mapDiscoveredResource(row)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// discoveryTypeToDB collapses domain kinds onto the six schema values.
func discoveryTypeToDB(kind domain.ResourceKind) string {
	switch kind {
	case domain.ResourceKindHostPath,
		domain.ResourceKindMount:
		return "host_path"
	case domain.ResourceKindDockerContainer:
		return "container"
	case domain.ResourceKindDockerVolume:
		return "volume"
	case domain.ResourceKindDockerBindMount:
		return "bind_mount"
	case domain.ResourceKindComposeProject:
		return "compose_project"
	case domain.ResourceKindPostgresDatabase,
		domain.ResourceKindMySQLDatabase,
		domain.ResourceKindMSSQLDatabase,
		domain.ResourceKindRedisDataset:
		return "database"
	default:
		return "host_path"
	}
}

// discoveryTypeFromDB restores the domain kind, preferring the stored
// original when present.
func discoveryTypeFromDB(dbType string, detail map[string]string) domain.ResourceKind {
	if kind, ok := detail[discoveryKindKey]; ok && domain.ResourceKind(kind) != "" {
		return domain.ResourceKind(kind)
	}
	switch dbType {
	case "container":
		return domain.ResourceKindDockerContainer
	case "volume":
		return domain.ResourceKindDockerVolume
	case "bind_mount":
		return domain.ResourceKindDockerBindMount
	case "database":
		return domain.ResourceKindPostgresDatabase
	case "compose_project":
		return domain.ResourceKindComposeProject
	default:
		return domain.ResourceKindHostPath
	}
}

func encodeDiscoveryDetails(kind domain.ResourceKind, detail map[string]string) ([]byte, error) {
	merged := make(map[string]string, len(detail)+1)
	for k, v := range detail {
		merged[k] = v
	}
	merged[discoveryKindKey] = string(kind)
	return encodeJSON(merged)
}

func mapDiscoveredResource(row gen.DiscoveredResource) (domain.DiscoveredResource, error) {
	detail := stringMap(row.Details)
	kind := discoveryTypeFromDB(row.ResourceType, detail)
	delete(detail, discoveryKindKey)
	name := row.DisplayName
	if name == "" {
		name = row.ResourceKey
	}
	return domain.DiscoveredResource{
		ID:           uuidString(row.ID),
		AgentID:      uuidString(row.AgentID),
		Kind:         kind,
		Name:         name,
		Detail:       detail,
		DiscoveredAt: timeOrZero(row.DiscoveredAt),
		LastSeenAt:   timeOrZero(row.LastSeenAt),
	}, nil
}
