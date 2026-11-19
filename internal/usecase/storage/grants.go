package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// Grant orchestration: short-lived, least-privilege presigned URLs per
// job+object. Upload keys are derived from the issuing job so a grant for
// job A cannot address job B's paths; downloads take explicit keys (restore
// reads objects written by earlier backup jobs) but must still be job-scoped
// in shape.

// Grant is a minted single-object access grant for the data plane.
type Grant struct {
	Method        string
	URL           string
	Headers       map[string]string
	ExpiresAt     time.Time
	DestinationID string
	JobID         string
	ObjectKey     string
}

// jobsDir mirrors the adapter key layout: <prefix>/jobs/<jobID>/<object>.
// (Duplicated here because the use case may depend only on domain.)
const jobsDir = "jobs"

// ScopeObjectKey builds the canonical key for one job's object.
func ScopeObjectKey(prefix, jobID, object string) (string, error) {
	if err := validGrantJobID(jobID); err != nil {
		return "", err
	}
	if err := validGrantObject(object); err != nil {
		return "", err
	}
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return jobsDir + "/" + jobID + "/" + object, nil
	}
	if err := validGrantPrefix(prefix); err != nil {
		return "", err
	}
	return prefix + "/" + jobsDir + "/" + jobID + "/" + object, nil
}

// ValidateJobKey reports whether key belongs to jobID under prefix.
func ValidateJobKey(prefix, jobID, key string) error {
	if err := validGrantJobID(jobID); err != nil {
		return err
	}
	if err := validGrantKey(key); err != nil {
		return err
	}
	dir := jobsDir + "/" + jobID
	if p := strings.Trim(prefix, "/"); p != "" {
		dir = p + "/" + dir
	}
	if strings.HasPrefix(key, dir+"/") && len(key) > len(dir)+1 {
		return nil
	}
	return fmt.Errorf("%w: object key %q is outside job %q scope", domain.ErrValidation, key, jobID)
}

// validateJobShape reports whether key has the jobs/<id>/<object> shape
// under prefix (any job).
func validateJobShape(prefix, key string) error {
	if err := validGrantKey(key); err != nil {
		return err
	}
	base := jobsDir
	if p := strings.Trim(prefix, "/"); p != "" {
		if err := validGrantPrefix(p); err != nil {
			return err
		}
		base = p + "/" + jobsDir
	}
	rest, ok := strings.CutPrefix(key, base+"/")
	if !ok || rest == "" {
		return fmt.Errorf("%w: object key %q is not job-scoped", domain.ErrValidation, key)
	}
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || parts[0] == "." || parts[0] == ".." {
		return fmt.Errorf("%w: object key %q is not job-scoped", domain.ErrValidation, key)
	}
	return nil
}

// IssueUploadGrant mints a PUT grant for object (a name relative to the
// job's scope, e.g. "chunk-0001.zst"). The stored key is derived from jobID
// so cross-job writes are impossible.
func (s *Service) IssueUploadGrant(ctx context.Context, destinationID, jobID, object string, ttl time.Duration) (Grant, error) {
	if s.store == nil {
		return Grant{}, fmt.Errorf("storage: issue upload grant: object storage not configured")
	}
	dest, err := s.requireDestination(ctx, destinationID)
	if err != nil {
		return Grant{}, err
	}
	key, err := ScopeObjectKey(dest.Prefix, jobID, object)
	if err != nil {
		return Grant{}, err
	}
	ttl, err = s.normalizeGrantTTL(ttl)
	if err != nil {
		return Grant{}, err
	}
	now := s.clock.Now().UTC()
	signed, err := s.store.PresignUpload(ctx, dest.ID, key, ttl)
	if err != nil {
		return Grant{}, fmt.Errorf("storage: issue upload grant: %w", err)
	}
	return Grant{
		Method:        "PUT",
		URL:           signed,
		Headers:       map[string]string{},
		ExpiresAt:     now.Add(ttl),
		DestinationID: dest.ID,
		JobID:         jobID,
		ObjectKey:     key,
	}, nil
}

// IssueDownloadGrant mints a GET grant for an explicit job-scoped key. The
// key is explicit (not derived from jobID) because restore jobs read objects
// written by earlier backup jobs; shape validation still confines reads to
// the jobs tree.
func (s *Service) IssueDownloadGrant(ctx context.Context, destinationID, jobID, objectKey string, ttl time.Duration) (Grant, error) {
	if s.store == nil {
		return Grant{}, fmt.Errorf("storage: issue download grant: object storage not configured")
	}
	if err := validGrantJobID(jobID); err != nil {
		return Grant{}, err
	}
	dest, err := s.requireDestination(ctx, destinationID)
	if err != nil {
		return Grant{}, err
	}
	if err := validateJobShape(dest.Prefix, objectKey); err != nil {
		return Grant{}, err
	}
	ttl, err = s.normalizeGrantTTL(ttl)
	if err != nil {
		return Grant{}, err
	}
	now := s.clock.Now().UTC()
	signed, err := s.store.PresignDownload(ctx, dest.ID, objectKey, ttl)
	if err != nil {
		return Grant{}, fmt.Errorf("storage: issue download grant: %w", err)
	}
	return Grant{
		Method:        "GET",
		URL:           signed,
		Headers:       map[string]string{},
		ExpiresAt:     now.Add(ttl),
		DestinationID: dest.ID,
		JobID:         jobID,
		ObjectKey:     objectKey,
	}, nil
}

// InitiateJobUpload starts a multipart upload for one job's object and
// returns the stored key plus the provider upload ID.
func (s *Service) InitiateJobUpload(ctx context.Context, destinationID, jobID, object string) (objectKey, uploadID string, err error) {
	if s.store == nil {
		return "", "", fmt.Errorf("storage: initiate upload: object storage not configured")
	}
	dest, err := s.requireDestination(ctx, destinationID)
	if err != nil {
		return "", "", err
	}
	key, err := ScopeObjectKey(dest.Prefix, jobID, object)
	if err != nil {
		return "", "", err
	}
	uploadID, err = s.store.InitiateMultipartUpload(ctx, dest.ID, key)
	if err != nil {
		return "", "", fmt.Errorf("storage: initiate upload: %w", err)
	}
	return key, uploadID, nil
}

// PresignJobParts mints PUT grants for the given part numbers of an
// in-flight multipart upload. The key must belong to jobID's scope.
func (s *Service) PresignJobParts(ctx context.Context, destinationID, jobID, objectKey, uploadID string, partNumbers []int, ttl time.Duration) ([]PartGrant, error) {
	if s.store == nil {
		return nil, fmt.Errorf("storage: presign parts: object storage not configured")
	}
	dest, err := s.requireDestination(ctx, destinationID)
	if err != nil {
		return nil, err
	}
	if err := ValidateJobKey(dest.Prefix, jobID, objectKey); err != nil {
		return nil, err
	}
	if strings.TrimSpace(uploadID) == "" {
		return nil, fmt.Errorf("%w: upload id is required", domain.ErrValidation)
	}
	if len(partNumbers) == 0 {
		return nil, fmt.Errorf("%w: at least one part number is required", domain.ErrValidation)
	}
	ttl, err = s.normalizeGrantTTL(ttl)
	if err != nil {
		return nil, err
	}
	out := make([]PartGrant, 0, len(partNumbers))
	for _, n := range partNumbers {
		if n < 1 || n > 10000 {
			return nil, fmt.Errorf("%w: part number %d out of range 1..10000", domain.ErrValidation, n)
		}
		g, err := s.store.PresignUploadPart(ctx, dest.ID, objectKey, uploadID, n, ttl)
		if err != nil {
			return nil, fmt.Errorf("storage: presign parts: %w", err)
		}
		g.PartNumber = n
		out = append(out, g)
	}
	return out, nil
}

// CompleteJobUpload finishes a multipart upload for one job's object.
func (s *Service) CompleteJobUpload(ctx context.Context, destinationID, jobID, objectKey, uploadID string, parts []MultipartPart) error {
	if s.store == nil {
		return fmt.Errorf("storage: complete upload: object storage not configured")
	}
	dest, err := s.requireDestination(ctx, destinationID)
	if err != nil {
		return err
	}
	if err := ValidateJobKey(dest.Prefix, jobID, objectKey); err != nil {
		return err
	}
	if strings.TrimSpace(uploadID) == "" {
		return fmt.Errorf("%w: upload id is required", domain.ErrValidation)
	}
	if len(parts) == 0 {
		return fmt.Errorf("%w: at least one part is required", domain.ErrValidation)
	}
	for _, p := range parts {
		if p.PartNumber < 1 || p.PartNumber > 10000 {
			return fmt.Errorf("%w: part number %d out of range 1..10000", domain.ErrValidation, p.PartNumber)
		}
		if strings.TrimSpace(p.ETag) == "" {
			return fmt.Errorf("%w: part %d ETag is required", domain.ErrValidation, p.PartNumber)
		}
	}
	if err := s.store.CompleteMultipartUpload(ctx, dest.ID, objectKey, uploadID, parts); err != nil {
		return fmt.Errorf("storage: complete upload: %w", err)
	}
	return nil
}

// AbortJobUpload cancels an in-flight multipart upload for one job's object.
func (s *Service) AbortJobUpload(ctx context.Context, destinationID, jobID, objectKey, uploadID string) error {
	if s.store == nil {
		return fmt.Errorf("storage: abort upload: object storage not configured")
	}
	dest, err := s.requireDestination(ctx, destinationID)
	if err != nil {
		return err
	}
	if err := ValidateJobKey(dest.Prefix, jobID, objectKey); err != nil {
		return err
	}
	if strings.TrimSpace(uploadID) == "" {
		return fmt.Errorf("%w: upload id is required", domain.ErrValidation)
	}
	if err := s.store.AbortMultipartUpload(ctx, dest.ID, objectKey, uploadID); err != nil {
		return fmt.Errorf("storage: abort upload: %w", err)
	}
	return nil
}

// VerifyJobObjects is the pre-success integrity gate: every expected object
// must exist with matching size/checksum. A false result (with details) or
// an error must both block success.
//
// INTEGRATION NOTE (recovery track): call this after the agent reports an
// upload set complete and before recording the snapshot as verified. Build
// ExpectedObject entries from the manifest (key, "algorithm:hex" checksum,
// size with CheckSize when the manifest records it).
func (s *Service) VerifyJobObjects(ctx context.Context, destinationID string, expected []ExpectedObject) (bool, []VerifyDetail, error) {
	if s.store == nil {
		return false, nil, fmt.Errorf("storage: verify objects: object storage not configured")
	}
	if len(expected) == 0 {
		return false, nil, fmt.Errorf("%w: nothing to verify", domain.ErrValidation)
	}
	dest, err := s.requireDestination(ctx, destinationID)
	if err != nil {
		return false, nil, err
	}
	ok, details, err := s.store.VerifyObjects(ctx, dest.ID, expected)
	if err != nil {
		return false, details, fmt.Errorf("storage: verify objects: %w", err)
	}
	return ok, details, nil
}

// requireDestination loads a destination or reports a validation error for
// a blank ID.
func (s *Service) requireDestination(ctx context.Context, destinationID string) (*domain.StorageDestination, error) {
	if strings.TrimSpace(destinationID) == "" {
		return nil, fmt.Errorf("%w: destination id is required", domain.ErrValidation)
	}
	d, err := s.repo.GetDestination(ctx, destinationID)
	if err != nil {
		return nil, fmt.Errorf("storage: %w", err)
	}
	return d, nil
}

// normalizeGrantTTL resolves a grant TTL: zero selects the default, values
// above the maximum are rejected so grants stay short-lived.
func (s *Service) normalizeGrantTTL(ttl time.Duration) (time.Duration, error) {
	if ttl <= 0 {
		return s.defaultTTL, nil
	}
	if ttl < time.Second {
		return 0, fmt.Errorf("%w: grant ttl must be at least 1s", domain.ErrValidation)
	}
	if ttl > s.maxTTL {
		return 0, fmt.Errorf("%w: grant ttl %s exceeds maximum %s", domain.ErrValidation, ttl, s.maxTTL)
	}
	return ttl, nil
}

func validGrantJobID(jobID string) error {
	if strings.TrimSpace(jobID) == "" {
		return fmt.Errorf("%w: job id is required", domain.ErrValidation)
	}
	if strings.ContainsAny(jobID, "/\\") || jobID == "." || jobID == ".." {
		return fmt.Errorf("%w: invalid job id %q", domain.ErrValidation, jobID)
	}
	return nil
}

func validGrantObject(object string) error {
	if object == "" || object[0] == '/' || strings.Contains(object, "\\") {
		return fmt.Errorf("%w: invalid object name %q", domain.ErrValidation, object)
	}
	for _, seg := range strings.Split(object, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("%w: invalid object name %q", domain.ErrValidation, object)
		}
	}
	return nil
}

func validGrantKey(key string) error {
	if key == "" || key[0] == '/' || strings.Contains(key, "\\") {
		return fmt.Errorf("%w: invalid object key %q", domain.ErrValidation, key)
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("%w: invalid object key %q", domain.ErrValidation, key)
		}
	}
	return nil
}

func validGrantPrefix(prefix string) error {
	for _, seg := range strings.Split(prefix, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("%w: invalid prefix %q", domain.ErrValidation, prefix)
		}
	}
	return nil
}
