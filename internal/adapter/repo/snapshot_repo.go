package repo

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vaultix/vaultix/db/gen"
	"github.com/vaultix/vaultix/internal/domain"
)

// SnapshotRepo implements domain.SnapshotRepository on
// snapshots/snapshot_manifests/snapshot_objects.
//
// Mapping notes: Selector maps to the name column and TargetType to
// storage_prefix (so catalog filtering by type keeps working); there is no
// separate prefix concept in domain. Status verified/failed map to the
// schema valid/invalid values (plus expired passthrough; foreign "deleted"
// reads as expired). BytesOriginal and SnapshotObject.Kind are not
// persisted. ManifestID is derived via reverse lookup (one manifest per
// snapshot); checksum "algorithm:hex" splits across checksum/checksum_alg
// (no prefix stores an empty algorithm). Manifest extras (format version,
// encryption, object count, verified-at) ride in the manifest content JSON.
type SnapshotRepo struct {
	baseRepo
}

// manifestContent is the snapshot_manifests.content JSON codec.
type manifestContent struct {
	FormatVersion int        `json:"format_version"`
	Encrypted     bool       `json:"encrypted"`
	KeyRef        string     `json:"key_ref,omitempty"`
	ObjectCount   int        `json:"object_count"`
	VerifiedAt    *time.Time `json:"verified_at,omitempty"`
}

// CreateSnapshot registers a snapshot. The ID is database-generated;
// CompletedAt is server-stamped from snapshot_time.
func (r *SnapshotRepo) CreateSnapshot(ctx context.Context, s *domain.Snapshot) error {
	if s == nil {
		return validationError("snapshot is required")
	}
	agentID, err := mustOptUUID(s.AgentID, "agent id")
	if err != nil {
		return err
	}
	policyID, err := mustOptUUID(s.PolicyID, "policy id")
	if err != nil {
		return err
	}
	jobID, err := mustOptUUID(s.JobID, "job id")
	if err != nil {
		return err
	}
	storageID, err := mustOptUUID(s.StorageID, "storage id")
	if err != nil {
		return err
	}
	state, err := snapshotStateToDB(s.Status)
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.CreateSnapshot(ctx, gen.CreateSnapshotParams{
		AgentID:                agentID,
		PolicyID:               policyID,
		PolicyVersion:          int4OrNull(s.PolicyVersion),
		BackupJobID:            jobID,
		Name:                   s.Selector,
		EncryptionEnabled:      s.Encrypted,
		EncryptionKeyReference: s.KeyRef,
		StorageDestinationID:   storageID,
		StoragePrefix:          string(s.TargetType),
	})
	if err != nil {
		return mapErr(err)
	}
	if state != row.State {
		row, err = r.q.SetSnapshotState(ctx, gen.SetSnapshotStateParams{ID: row.ID, State: state})
		if err != nil {
			return mapErr(err)
		}
	}
	if s.ExpiresAt != nil {
		if err := r.q.SetSnapshotExpiry(ctx, gen.SetSnapshotExpiryParams{
			ID: row.ID, ExpiresAt: timestamptzPtr(s.ExpiresAt),
		}); err != nil {
			return mapErr(err)
		}
	}
	s.ID = uuidString(row.ID)
	completed := timeOrZero(row.SnapshotTime)
	s.CompletedAt = &completed
	s.CreatedAt = timeOrZero(row.CreatedAt)
	return nil
}

// GetSnapshot returns one snapshot, resolving its manifest when present.
func (r *SnapshotRepo) GetSnapshot(ctx context.Context, id string) (*domain.Snapshot, error) {
	sid, err := mustParseUUID(id, "snapshot id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetSnapshot(ctx, gen.GetSnapshotParams{ID: sid})
	if err != nil {
		return nil, mapErr(err)
	}
	s, err := mapSnapshot(row)
	if err != nil {
		return nil, err
	}
	s.ManifestID = r.manifestIDForSnapshot(ctx, sid)
	return &s, nil
}

// UpdateSnapshot rewrites a snapshot's mutable catalog state.
func (r *SnapshotRepo) UpdateSnapshot(ctx context.Context, s *domain.Snapshot) error {
	if s == nil {
		return validationError("snapshot is required")
	}
	sid, err := mustParseUUID(s.ID, "snapshot id")
	if err != nil {
		return err
	}
	state, err := snapshotStateToDB(s.Status)
	if err != nil {
		return err
	}
	storageID, err := mustOptUUID(s.StorageID, "storage id")
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	_, err = r.q.UpdateSnapshot(ctx, gen.UpdateSnapshotParams{
		ID:                     sid,
		Name:                   s.Selector,
		State:                  state,
		BytesStored:            uint64OrZero(s.BytesStored),
		FileCount:              int64OrZero(s.ObjectCount),
		EncryptionEnabled:      s.Encrypted,
		EncryptionKeyReference: s.KeyRef,
		StorageDestinationID:   storageID,
		StoragePrefix:          string(s.TargetType),
		SnapshotTime:           timestamptzPtr(s.CompletedAt),
		ExpiresAt:              timestamptzPtr(s.ExpiresAt),
	})
	if err != nil {
		return mapErr(err)
	}
	return nil
}

// DeleteSnapshot removes a snapshot (manifest and objects cascade) after
// verifying it exists.
func (r *SnapshotRepo) DeleteSnapshot(ctx context.Context, id string) error {
	sid, err := mustParseUUID(id, "snapshot id")
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	if _, err := r.q.GetSnapshot(ctx, gen.GetSnapshotParams{ID: sid}); err != nil {
		return mapErr(err)
	}
	if err := r.q.DeleteSnapshot(ctx, gen.DeleteSnapshotParams{ID: sid}); err != nil {
		return mapErr(err)
	}
	return nil
}

// ListSnapshots returns one page of the catalog narrowed by the filter.
// Restorable restricts to verified snapshots with a manifest present.
func (r *SnapshotRepo) ListSnapshots(ctx context.Context, opts domain.ListOptions, filter domain.SnapshotFilter) (domain.ListResult[domain.Snapshot], error) {
	agentID, err := mustOptUUID(filter.AgentID, "agent id")
	if err != nil {
		return domain.ListResult[domain.Snapshot]{}, err
	}
	policyID, err := mustOptUUID(filter.PolicyID, "policy id")
	if err != nil {
		return domain.ListResult[domain.Snapshot]{}, err
	}
	limit, offset := page(opts)
	params := gen.ListSnapshotsFilteredParams{
		Column1: agentID,
		Column2: policyID,
		Column3: "",
		Column4: string(filter.TargetType),
		Column5: timestamptzPtr(filter.Since),
		Column6: timestamptzPtr(filter.Until),
		Column7: filter.Restorable,
		Limit:   limit,
		Offset:  offset,
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	rows, err := r.q.ListSnapshotsFiltered(ctx, params)
	if err != nil {
		return domain.ListResult[domain.Snapshot]{}, mapErr(err)
	}
	total, err := r.q.CountSnapshotsFiltered(ctx, gen.CountSnapshotsFilteredParams{
		Column1: params.Column1,
		Column2: params.Column2,
		Column3: params.Column3,
		Column4: params.Column4,
		Column5: params.Column5,
		Column6: params.Column6,
		Column7: params.Column7,
	})
	if err != nil {
		return domain.ListResult[domain.Snapshot]{}, mapErr(err)
	}
	out := make([]domain.Snapshot, 0, len(rows))
	for _, row := range rows {
		s, err := mapSnapshot(row)
		if err != nil {
			return domain.ListResult[domain.Snapshot]{}, err
		}
		s.ManifestID = r.manifestIDForSnapshot(ctx, row.ID)
		out = append(out, s)
	}
	return domain.ListResult[domain.Snapshot]{Items: out, Total: int(total)}, nil
}

// PutManifest upserts the manifest of a snapshot (one per snapshot).
func (r *SnapshotRepo) PutManifest(ctx context.Context, m *domain.SnapshotManifest) error {
	if m == nil {
		return validationError("manifest is required")
	}
	sid, err := mustParseUUID(m.SnapshotID, "snapshot id")
	if err != nil {
		return err
	}
	content, err := encodeJSON(manifestContent{
		FormatVersion: m.FormatVersion,
		Encrypted:     m.Encrypted,
		KeyRef:        m.KeyRef,
		ObjectCount:   m.ObjectCount,
		VerifiedAt:    m.VerifiedAt,
	})
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.UpsertSnapshotManifest(ctx, gen.UpsertSnapshotManifestParams{
		SnapshotID:        sid,
		ManifestHash:      m.Checksum,
		ManifestSizeBytes: uint64ToInt64(m.BytesStored),
		StorageKey:        m.ObjectKey,
		Content:           content,
	})
	if err != nil {
		return mapErr(err)
	}
	m.ID = uuidString(row.ID)
	m.CreatedAt = timeOrZero(row.CreatedAt)
	return nil
}

// GetManifest returns the manifest of a snapshot.
func (r *SnapshotRepo) GetManifest(ctx context.Context, snapshotID string) (*domain.SnapshotManifest, error) {
	sid, err := mustParseUUID(snapshotID, "snapshot id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetManifestBySnapshot(ctx, gen.GetManifestBySnapshotParams{SnapshotID: sid})
	if err != nil {
		return nil, mapErr(err)
	}
	m, err := mapManifest(row)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// PutObjects upserts a manifest's object set, atomically. An empty set is
// a no-op.
func (r *SnapshotRepo) PutObjects(ctx context.Context, objects []domain.SnapshotObject) error {
	encoded := make([]gen.UpsertSnapshotObjectParams, 0, len(objects))
	for i := range objects {
		sid, err := mustParseUUID(objects[i].SnapshotID, "snapshot id")
		if err != nil {
			return err
		}
		if objects[i].ObjectKey == "" {
			return validationError("object key is required")
		}
		sum, alg := splitChecksum(objects[i].Checksum)
		encoded = append(encoded, gen.UpsertSnapshotObjectParams{
			SnapshotID:  sid,
			ObjectKey:   objects[i].ObjectKey,
			SizeBytes:   uint64ToInt64(objects[i].Size),
			Checksum:    sum,
			ChecksumAlg: alg,
			PartIndex:   int32OrZero(objects[i].Sequence),
		})
	}
	if len(encoded) == 0 {
		return nil
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	return mapErr(r.inTx(ctx, func(q *gen.Queries) error {
		for i, params := range encoded {
			row, err := q.UpsertSnapshotObject(ctx, params)
			if err != nil {
				return err
			}
			objects[i].ID = uuidString(row.ID)
			objects[i].CreatedAt = timeOrZero(row.CreatedAt)
		}
		return nil
	}))
}

// ListObjects returns the objects of a manifest, ordered for reassembly.
func (r *SnapshotRepo) ListObjects(ctx context.Context, manifestID string) ([]domain.SnapshotObject, error) {
	mid, err := mustParseUUID(manifestID, "manifest id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	manifest, err := r.q.GetManifestByID(ctx, gen.GetManifestByIDParams{ID: mid})
	if err != nil {
		return nil, mapErr(err)
	}
	rows, err := r.q.ListObjectsBySnapshot(ctx, gen.ListObjectsBySnapshotParams{SnapshotID: manifest.SnapshotID})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]domain.SnapshotObject, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapSnapshotObject(manifestID, row))
	}
	return out, nil
}

// manifestIDForSnapshot reverse-resolves a snapshot's manifest; snapshots
// without one resolve to "".
func (r *SnapshotRepo) manifestIDForSnapshot(ctx context.Context, snapshotID pgtype.UUID) string {
	row, err := r.q.GetManifestBySnapshot(ctx, gen.GetManifestBySnapshotParams{SnapshotID: snapshotID})
	if err != nil {
		return ""
	}
	return uuidString(row.ID)
}

// snapshotStateToDB maps domain statuses onto the schema check values.
func snapshotStateToDB(status domain.SnapshotStatus) (string, error) {
	switch status {
	case "", domain.SnapshotPending:
		return "pending", nil
	case domain.SnapshotVerifying:
		return "verifying", nil
	case domain.SnapshotVerified:
		return "valid", nil
	case domain.SnapshotFailed:
		return "invalid", nil
	case domain.SnapshotExpired:
		return "expired", nil
	default:
		return "", validationError("unknown snapshot status")
	}
}

// snapshotStateFromDB maps schema states back; foreign "deleted" reads as
// expired.
func snapshotStateFromDB(state string) (domain.SnapshotStatus, error) {
	switch state {
	case "pending":
		return domain.SnapshotPending, nil
	case "verifying":
		return domain.SnapshotVerifying, nil
	case "valid":
		return domain.SnapshotVerified, nil
	case "invalid":
		return domain.SnapshotFailed, nil
	case "expired", "deleted":
		return domain.SnapshotExpired, nil
	default:
		return "", validationError("unknown stored snapshot state")
	}
}

func mapSnapshot(row gen.Snapshot) (domain.Snapshot, error) {
	status, err := snapshotStateFromDB(row.State)
	if err != nil {
		return domain.Snapshot{}, err
	}
	var targetType domain.TargetType
	if domain.TargetType(row.StoragePrefix).Valid() {
		targetType = domain.TargetType(row.StoragePrefix)
	}
	return domain.Snapshot{
		ID:            uuidString(row.ID),
		AgentID:       uuidString(row.AgentID),
		PolicyID:      uuidString(row.PolicyID),
		PolicyVersion: intOrZero(row.PolicyVersion),
		JobID:         uuidString(row.BackupJobID),
		TargetType:    targetType,
		Selector:      row.Name,
		Status:        status,
		BytesStored:   uint64Ptr(row.BytesStored),
		ObjectCount:   int(row.FileCount),
		Encrypted:     row.EncryptionEnabled,
		KeyRef:        row.EncryptionKeyReference,
		StorageID:     uuidString(row.StorageDestinationID),
		CompletedAt:   timePtr(row.SnapshotTime),
		ExpiresAt:     timePtr(row.ExpiresAt),
		CreatedAt:     timeOrZero(row.CreatedAt),
	}, nil
}

func mapManifest(row gen.SnapshotManifest) (domain.SnapshotManifest, error) {
	var content manifestContent
	if err := decodeJSON(row.Content, &content); err != nil {
		return domain.SnapshotManifest{}, err
	}
	return domain.SnapshotManifest{
		ID:            uuidString(row.ID),
		SnapshotID:    uuidString(row.SnapshotID),
		FormatVersion: content.FormatVersion,
		ObjectKey:     row.StorageKey,
		Checksum:      row.ManifestHash,
		Encrypted:     content.Encrypted,
		KeyRef:        content.KeyRef,
		ObjectCount:   content.ObjectCount,
		BytesStored:   uint64FromInt64(row.ManifestSizeBytes),
		VerifiedAt:    content.VerifiedAt,
		CreatedAt:     timeOrZero(row.CreatedAt),
	}, nil
}

func mapSnapshotObject(manifestID string, row gen.SnapshotObject) domain.SnapshotObject {
	return domain.SnapshotObject{
		ID:         uuidString(row.ID),
		ManifestID: manifestID,
		SnapshotID: uuidString(row.SnapshotID),
		ObjectKey:  row.ObjectKey,
		Size:       uint64FromInt64(row.SizeBytes),
		Checksum:   joinChecksum(row.Checksum, row.ChecksumAlg),
		Sequence:   int(row.PartIndex),
		CreatedAt:  timeOrZero(row.CreatedAt),
	}
}

// splitChecksum divides "algorithm:hex" for the checksum columns; a bare
// value stores an empty algorithm so the join round-trips.
func splitChecksum(checksum string) (sum, alg string) {
	if alg, sum, ok := strings.Cut(checksum, ":"); ok {
		return sum, alg
	}
	return checksum, ""
}

// joinChecksum reassembles "algorithm:hex", omitting the prefix when the
// algorithm is empty.
func joinChecksum(sum, alg string) string {
	if alg == "" {
		return sum
	}
	return alg + ":" + sum
}

func int64OrZero(n int) int64 {
	if n < 0 {
		return 0
	}
	return int64(n)
}

func uint64ToInt64(n uint64) int64 {
	if n > uint64(1<<63-1) {
		return 1<<63 - 1
	}
	return int64(n) //nolint:gosec // clamped above
}

func uint64FromInt64(n int64) uint64 {
	if n < 0 {
		return 0
	}
	return uint64(n) //nolint:gosec // guarded above
}
