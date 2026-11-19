package s3

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client defaults and grant bounds.
const (
	// DefaultTimeout bounds every S3 call lacking a context deadline.
	DefaultTimeout = 10 * time.Second
	// DefaultRegion is used when the destination names none.
	DefaultRegion = "us-east-1"
	// DefaultGrantTTL applies when a grant request names no TTL.
	DefaultGrantTTL = 15 * time.Minute
	// DefaultMaxGrantTTL caps grant lifetime (short-lived, least privilege).
	DefaultMaxGrantTTL = time.Hour
	// DefaultMaxAttempts is 1 initial try + 3 retries on transient failures.
	DefaultMaxAttempts = 4
	// DefaultRetryInitialInterval seeds the exponential backoff between tries.
	DefaultRetryInitialInterval = 200 * time.Millisecond
)

// Config describes one S3-compatible destination endpoint. Credentials are
// the destination's own configured keys (resolved from its encrypted secret
// profile by the wiring layer); the client mints only short-lived scoped
// grants from them and never logs them.
type Config struct {
	// Endpoint overrides the S3 endpoint (e.g. https://minio:9000); empty
	// selects the AWS default for Region.
	Endpoint string
	Region   string
	Bucket   string
	// Prefix scopes keys (see ScopeKey); may be empty.
	Prefix          string
	AccessKeyID     string
	SecretAccessKey string
	UsePathStyle    bool
	// Timeout bounds each call without a context deadline.
	Timeout time.Duration
	// HTTPClient performs requests; nil uses a shared default.
	HTTPClient *http.Client
	// MaxAttempts caps tries per call (default DefaultMaxAttempts).
	MaxAttempts int
	// RetryInitialInterval seeds retry backoff (default 200ms).
	RetryInitialInterval time.Duration
	// MaxTTL caps presigned grant lifetime (default DefaultMaxGrantTTL).
	MaxTTL time.Duration
	// Now supplies time; nil uses time.Now (test hook).
	Now func() time.Time
}

// Validate checks the static configuration invariants.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Bucket) == "" {
		return fmt.Errorf("s3: bucket is required")
	}
	if strings.TrimSpace(c.AccessKeyID) == "" {
		return fmt.Errorf("s3: access key id is required")
	}
	if c.SecretAccessKey == "" {
		return fmt.Errorf("s3: secret access key is required")
	}
	if c.Endpoint != "" {
		u, err := url.Parse(c.Endpoint)
		if err != nil {
			return fmt.Errorf("s3: invalid endpoint: %w", err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("s3: endpoint scheme must be http or https")
		}
		if u.Host == "" {
			return fmt.Errorf("s3: endpoint host is required")
		}
		if u.User != nil {
			return fmt.Errorf("s3: endpoint must not embed credentials")
		}
	}
	return nil
}

// withDefaults fills zero values; the config must already validate.
func (c Config) withDefaults() Config {
	if strings.TrimSpace(c.Region) == "" {
		c.Region = DefaultRegion
	}
	if c.Timeout <= 0 {
		c.Timeout = DefaultTimeout
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{}
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = DefaultMaxAttempts
	}
	if c.RetryInitialInterval <= 0 {
		c.RetryInitialInterval = DefaultRetryInitialInterval
	}
	if c.MaxTTL <= 0 {
		c.MaxTTL = DefaultMaxGrantTTL
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

// normalizeTTL resolves a grant TTL: zero selects the default, values above
// the maximum are rejected so grants stay short-lived.
func normalizeTTL(ttl, max time.Duration) (time.Duration, error) {
	if ttl <= 0 {
		return DefaultGrantTTL, nil
	}
	if ttl < time.Second {
		return 0, fmt.Errorf("s3: grant ttl must be at least 1s")
	}
	if ttl > max {
		return 0, fmt.Errorf("s3: grant ttl %s exceeds maximum %s", ttl, max)
	}
	return ttl, nil
}
