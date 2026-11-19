package storage

import (
	"context"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// ObjectStore is the storage I/O seam: domain.ObjectStorage plus the
// multipart-upload and integrity-verification helpers implemented by the S3
// adapter's Registry. The use case orchestrates through this port and never
// touches transport details.
type ObjectStore interface {
	domain.ObjectStorage
	// InitiateMultipartUpload starts a multipart upload for objectKey and
	// returns the provider upload ID.
	InitiateMultipartUpload(ctx context.Context, destinationID, objectKey string) (string, error)
	// PresignUploadPart mints a short-lived PUT grant for one part.
	PresignUploadPart(ctx context.Context, destinationID, objectKey, uploadID string, partNumber int, ttl time.Duration) (PartGrant, error)
	// CompleteMultipartUpload finishes a multipart upload.
	CompleteMultipartUpload(ctx context.Context, destinationID, objectKey, uploadID string, parts []MultipartPart) error
	// AbortMultipartUpload cancels an in-flight multipart upload.
	AbortMultipartUpload(ctx context.Context, destinationID, objectKey, uploadID string) error
	// VerifyObjects HEADs each expected key and compares stored
	// size/checksum; it is the pre-success integrity gate.
	VerifyObjects(ctx context.Context, destinationID string, expected []ExpectedObject) (bool, []VerifyDetail, error)
}

// PartGrant is a short-lived PUT grant for one multipart part.
type PartGrant struct {
	PartNumber int
	Method     string
	URL        string
	ExpiresAt  time.Time
}

// MultipartPart identifies one uploaded part for completion.
type MultipartPart struct {
	PartNumber int
	ETag       string
}

// ExpectedObject is one manifest entry to verify before success.
type ExpectedObject struct {
	ObjectKey string
	Checksum  string // "algorithm:hex"
	// Size is compared only when CheckSize is set.
	Size      uint64
	CheckSize bool
}

// VerifyDetail is the per-object verification outcome.
type VerifyDetail struct {
	ObjectKey string
	OK        bool
	Reason    string
}
