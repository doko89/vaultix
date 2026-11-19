package s3

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"
)

// Multipart orchestration for large objects (PRD §7.12): the Manager
// initiates and completes uploads while the agent PUTs each part directly to
// a presigned part URL. Part grants are scoped to the job's key exactly like
// single-object grants.

// CompletedPart identifies one uploaded part for completion.
type CompletedPart struct {
	PartNumber int
	ETag       string
}

type initiateMultipartResult struct {
	XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
	UploadID string   `xml:"UploadId"`
}

type completeMultipartBody struct {
	XMLName xml.Name            `xml:"CompleteMultipartUpload"`
	Xmlns   string              `xml:"xmlns,attr,omitempty"`
	Parts   []completePartEntry `xml:"Part"`
}

type completePartEntry struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

// InitiateMultipartUpload starts a multipart upload for key, which must
// belong to jobID's scope, and returns the provider upload ID.
func (c *Client) InitiateMultipartUpload(ctx context.Context, jobID, key string) (string, error) {
	if err := ValidateScopedKey(c.prefix, jobID, key); err != nil {
		return "", err
	}
	return c.initiateMultipartUpload(ctx, key)
}

// initiateMultipartUpload starts a multipart upload for a job-scoped key
// without binding a specific job (port-level use).
func (c *Client) initiateMultipartUpload(ctx context.Context, key string) (string, error) {
	if err := ValidateJobScopedShape(c.prefix, key); err != nil {
		return "", err
	}
	host, rawPath := c.target(key)
	rawURL := c.scheme + "://" + host + encodePath(rawPath) + "?uploads="
	resp, err := c.roundTrip(ctx, http.MethodPost, rawURL, nil, "")
	if err != nil {
		return "", fmt.Errorf("s3: initiate multipart upload: %w", err)
	}
	return parseInitiateResult(resp.body)
}

func parseInitiateResult(body []byte) (string, error) {
	var out initiateMultipartResult
	if err := xml.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("s3: initiate multipart upload: decode response: %w", err)
	}
	if out.UploadID == "" {
		return "", fmt.Errorf("s3: initiate multipart upload: empty upload id")
	}
	return out.UploadID, nil
}

// PresignUploadPart mints a short-lived PUT grant for one part. Part numbers
// run 1..10000 per the S3 API.
func (c *Client) PresignUploadPart(ctx context.Context, jobID, key, uploadID string, partNumber int, ttl time.Duration) (Grant, error) {
	if err := ctx.Err(); err != nil {
		return Grant{}, fmt.Errorf("s3: presign part: %w", err)
	}
	if err := ValidateScopedKey(c.prefix, jobID, key); err != nil {
		return Grant{}, err
	}
	if uploadID == "" {
		return Grant{}, fmt.Errorf("s3: presign part: upload id is required")
	}
	if partNumber < 1 || partNumber > 10000 {
		return Grant{}, fmt.Errorf("s3: presign part: part number %d out of range 1..10000", partNumber)
	}
	sub := map[string]string{
		"partNumber": strconv.Itoa(partNumber),
		"uploadId":   uploadID,
	}
	g, err := c.presign(http.MethodPut, key, sub, ttl)
	if err != nil {
		return Grant{}, fmt.Errorf("s3: presign part: %w", err)
	}
	return g, nil
}

// CompleteMultipartUpload finishes an upload previously started with
// InitiateMultipartUpload. Parts are sorted by number before submission.
func (c *Client) CompleteMultipartUpload(ctx context.Context, key, uploadID string, parts []CompletedPart) error {
	if err := ValidateJobScopedShape(c.prefix, key); err != nil {
		return err
	}
	if uploadID == "" {
		return fmt.Errorf("s3: complete multipart upload: upload id is required")
	}
	if len(parts) == 0 {
		return fmt.Errorf("s3: complete multipart upload: at least one part is required")
	}
	ordered := slices.Clone(parts)
	slices.SortFunc(ordered, func(a, b CompletedPart) int { return a.PartNumber - b.PartNumber })
	body := completeMultipartBody{}
	for _, p := range ordered {
		if p.PartNumber < 1 || p.PartNumber > 10000 {
			return fmt.Errorf("s3: complete multipart upload: part number %d out of range 1..10000", p.PartNumber)
		}
		if p.ETag == "" {
			return fmt.Errorf("s3: complete multipart upload: part %d ETag is required", p.PartNumber)
		}
		body.Parts = append(body.Parts, completePartEntry{PartNumber: p.PartNumber, ETag: p.ETag})
	}
	payload, err := xml.Marshal(body)
	if err != nil {
		return fmt.Errorf("s3: complete multipart upload: encode request: %w", err)
	}
	host, rawPath := c.target(key)
	rawURL := c.scheme + "://" + host + encodePath(rawPath) + "?uploadId=" + encodeComponent(uploadID, false)
	if _, err := c.roundTrip(ctx, http.MethodPost, rawURL, payload, "application/xml"); err != nil {
		return fmt.Errorf("s3: complete multipart upload: %w", err)
	}
	return nil
}

// AbortMultipartUpload cancels an in-flight multipart upload, releasing
// staged parts.
func (c *Client) AbortMultipartUpload(ctx context.Context, key, uploadID string) error {
	if err := ValidateJobScopedShape(c.prefix, key); err != nil {
		return err
	}
	if uploadID == "" {
		return fmt.Errorf("s3: abort multipart upload: upload id is required")
	}
	host, rawPath := c.target(key)
	rawURL := c.scheme + "://" + host + encodePath(rawPath) + "?uploadId=" + encodeComponent(uploadID, false)
	if _, err := c.roundTrip(ctx, http.MethodDelete, rawURL, nil, ""); err != nil {
		return fmt.Errorf("s3: abort multipart upload: %w", err)
	}
	return nil
}
