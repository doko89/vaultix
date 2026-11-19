// Package s3 implements S3-compatible object storage access for the Manager.
//
// Client is a per-destination SigV4 client over plain net/http with endpoint
// override for MinIO and other S3-compatible services. It mints short-lived,
// job-scoped presigned PUT/GET grants, orchestrates multipart uploads,
// verifies object integrity via HEAD + checksum comparison, and tests
// destination connectivity. Registry resolves a destination ID to a Client
// and implements domain.ObjectStorage plus the storage use-case's extended
// ObjectStore port.
//
// The package uses stdlib HTTP with SigV4 signing so no extra dependency is
// required; presigning semantics match service/s3 query authentication.
// Long-lived credentials are only the destination's own configured keys and
// are never logged; signature/credential material is redacted from errors.
package s3
