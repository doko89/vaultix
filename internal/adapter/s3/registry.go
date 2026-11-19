package s3

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
	ustorage "github.com/vaultix/vaultix/internal/usecase/storage"
)

// Registry resolves a destination ID to a per-destination Client and
// implements domain.ObjectStorage plus the storage use-case's extended
// ObjectStore port (multipart + verification). Clients share one HTTP
// transport for connection pooling; destination configs (including rotated
// credentials) resolve fresh on every call.
//
// The ResolveFunc is provided by cmd wiring: it loads the destination row,
// opens its credential profile via the SecretBox, and returns the Config.
// Resolve implementations must not include secrets in returned errors.
type Registry struct {
	resolve    ResolveFunc
	httpClient *http.Client
}

// ResolveFunc maps a destination ID to a Client config.
type ResolveFunc func(ctx context.Context, destinationID string) (Config, error)

// NewRegistry builds a Registry. A nil httpClient selects a shared default
// with no client-level timeout (per-call context timeouts govern instead).
func NewRegistry(resolve ResolveFunc, httpClient *http.Client) *Registry {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &Registry{resolve: resolve, httpClient: httpClient}
}

var (
	_ domain.ObjectStorage = (*Registry)(nil)
	_ ustorage.ObjectStore = (*Registry)(nil)
)

// clientFor resolves destinationID to a ready Client.
func (r *Registry) clientFor(ctx context.Context, destinationID string) (*Client, error) {
	if r.resolve == nil {
		return nil, fmt.Errorf("s3: no destination resolver configured")
	}
	if strings.TrimSpace(destinationID) == "" {
		return nil, fmt.Errorf("s3: destination id is required")
	}
	cfg, err := r.resolve(ctx, destinationID)
	if err != nil {
		return nil, fmt.Errorf("s3: resolve destination: %w", err)
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = r.httpClient
	}
	c, err := New(cfg)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// PresignUpload returns a short-lived PUT URL for one job-scoped object.
func (r *Registry) PresignUpload(ctx context.Context, destinationID, objectKey string, ttl time.Duration) (string, error) {
	c, err := r.clientFor(ctx, destinationID)
	if err != nil {
		return "", err
	}
	g, err := c.presignPutShaped(objectKey, ttl)
	if err != nil {
		return "", err
	}
	return g.URL, nil
}

// PresignDownload returns a short-lived GET URL for one job-scoped object.
func (r *Registry) PresignDownload(ctx context.Context, destinationID, objectKey string, ttl time.Duration) (string, error) {
	c, err := r.clientFor(ctx, destinationID)
	if err != nil {
		return "", err
	}
	g, err := c.presignGetShaped(objectKey, ttl)
	if err != nil {
		return "", err
	}
	return g.URL, nil
}

// Head returns stored size/checksum for verification.
func (r *Registry) Head(ctx context.Context, destinationID, objectKey string) (domain.ObjectMeta, error) {
	c, err := r.clientFor(ctx, destinationID)
	if err != nil {
		return domain.ObjectMeta{}, err
	}
	return c.Head(ctx, objectKey)
}

// Delete removes one object (retention pruning).
func (r *Registry) Delete(ctx context.Context, destinationID, objectKey string) error {
	c, err := r.clientFor(ctx, destinationID)
	if err != nil {
		return err
	}
	return c.Delete(ctx, objectKey)
}

// Ping tests destination connectivity and credentials.
func (r *Registry) Ping(ctx context.Context, destinationID string) error {
	c, err := r.clientFor(ctx, destinationID)
	if err != nil {
		return err
	}
	return c.Ping(ctx)
}

// InitiateMultipartUpload starts a multipart upload for a job-scoped key.
func (r *Registry) InitiateMultipartUpload(ctx context.Context, destinationID, objectKey string) (string, error) {
	c, err := r.clientFor(ctx, destinationID)
	if err != nil {
		return "", err
	}
	return c.initiateMultipartUpload(ctx, objectKey)
}

// PresignUploadPart mints a short-lived PUT grant for one part.
func (r *Registry) PresignUploadPart(ctx context.Context, destinationID, objectKey, uploadID string, partNumber int, ttl time.Duration) (ustorage.PartGrant, error) {
	c, err := r.clientFor(ctx, destinationID)
	if err != nil {
		return ustorage.PartGrant{}, err
	}
	if err := ValidateJobScopedShape(c.prefix, objectKey); err != nil {
		return ustorage.PartGrant{}, err
	}
	if uploadID == "" {
		return ustorage.PartGrant{}, fmt.Errorf("s3: presign part: upload id is required")
	}
	if partNumber < 1 || partNumber > 10000 {
		return ustorage.PartGrant{}, fmt.Errorf("s3: presign part: part number %d out of range 1..10000", partNumber)
	}
	ttl, err = normalizeTTL(ttl, c.maxTTL)
	if err != nil {
		return ustorage.PartGrant{}, err
	}
	g, err := c.presign(http.MethodPut, objectKey, map[string]string{
		"partNumber": strconv.Itoa(partNumber),
		"uploadId":   uploadID,
	}, ttl)
	if err != nil {
		return ustorage.PartGrant{}, fmt.Errorf("s3: presign part: %w", err)
	}
	return ustorage.PartGrant{
		PartNumber: partNumber,
		Method:     g.Method,
		URL:        g.URL,
		ExpiresAt:  g.ExpiresAt,
	}, nil
}

// CompleteMultipartUpload finishes a multipart upload.
func (r *Registry) CompleteMultipartUpload(ctx context.Context, destinationID, objectKey, uploadID string, parts []ustorage.MultipartPart) error {
	c, err := r.clientFor(ctx, destinationID)
	if err != nil {
		return err
	}
	converted := make([]CompletedPart, 0, len(parts))
	for _, p := range parts {
		converted = append(converted, CompletedPart{PartNumber: p.PartNumber, ETag: p.ETag, SHA256: p.SHA256, SizeBytes: p.SizeBytes})
	}
	return c.CompleteMultipartUpload(ctx, objectKey, uploadID, converted)
}

// AbortMultipartUpload cancels an in-flight multipart upload.
func (r *Registry) AbortMultipartUpload(ctx context.Context, destinationID, objectKey, uploadID string) error {
	c, err := r.clientFor(ctx, destinationID)
	if err != nil {
		return err
	}
	return c.AbortMultipartUpload(ctx, objectKey, uploadID)
}

// VerifyObjects HEADs each expected key and compares stored size/checksum.
func (r *Registry) VerifyObjects(ctx context.Context, destinationID string, expected []ustorage.ExpectedObject) (bool, []ustorage.VerifyDetail, error) {
	c, err := r.clientFor(ctx, destinationID)
	if err != nil {
		return false, nil, err
	}
	converted := make([]ExpectedObject, 0, len(expected))
	for _, e := range expected {
		converted = append(converted, ExpectedObject{
			Key:       e.ObjectKey,
			Checksum:  e.Checksum,
			Size:      e.Size,
			CheckSize: e.CheckSize,
		})
	}
	ok, results, err := c.VerifyObjects(ctx, converted)
	if err != nil {
		return false, nil, err
	}
	details := make([]ustorage.VerifyDetail, 0, len(results))
	for _, res := range results {
		details = append(details, ustorage.VerifyDetail{
			ObjectKey: res.ObjectKey,
			OK:        res.OK,
			Reason:    res.Reason,
		})
	}
	return ok, details, nil
}
