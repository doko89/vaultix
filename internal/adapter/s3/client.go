package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/vaultix/vaultix/internal/domain"
)

// Grant is a short-lived, single-object access grant minted for the data
// plane. The URL carries SigV4 query authentication and expires at
// ExpiresAt; it must be delivered to the authorized agent only and never
// logged (see RedactURL).
type Grant struct {
	Method    string
	URL       string
	Headers   map[string]string
	ExpiresAt time.Time
	Bucket    string
	ObjectKey string
}

// Client is a per-destination S3-compatible client. Every I/O call carries a
// context timeout (the configured default when the caller sets no deadline)
// and retries transient failures with exponential backoff.
type Client struct {
	scheme        string
	baseHost      string
	region        string
	bucket        string
	prefix        string
	access        string
	secret        string
	virtualHosted bool
	timeout       time.Duration
	httpClient    *http.Client
	maxTries      uint
	retryInitial  time.Duration
	maxTTL        time.Duration
	now           func() time.Time
}

// New builds a Client from cfg.
func New(cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg = cfg.withDefaults()
	scheme := "https"
	baseHost := "s3." + cfg.Region + ".amazonaws.com"
	if cfg.Endpoint != "" {
		u, _ := url.Parse(cfg.Endpoint) // validated above
		scheme = u.Scheme
		baseHost = u.Host
	}
	c := &Client{
		scheme:       scheme,
		baseHost:     baseHost,
		region:       cfg.Region,
		bucket:       cfg.Bucket,
		prefix:       strings.Trim(cfg.Prefix, "/"),
		access:       cfg.AccessKeyID,
		secret:       cfg.SecretAccessKey,
		timeout:      cfg.Timeout,
		httpClient:   cfg.HTTPClient,
		maxTries:     uint(cfg.MaxAttempts), //nolint:gosec // validated positive
		retryInitial: cfg.RetryInitialInterval,
		maxTTL:       cfg.MaxTTL,
		now:          cfg.Now,
	}
	// Virtual-hosted style needs wildcard DNS; IP/literal hosts and MinIO
	// style endpoints without it must use path style.
	c.virtualHosted = !cfg.UsePathStyle && dnsCompatibleBucket(cfg.Bucket) && !isIPHost(baseHost)
	return c, nil
}

// Prefix returns the destination key prefix the client scopes grants under.
func (c *Client) Prefix() string { return c.prefix }

// Bucket returns the destination bucket.
func (c *Client) Bucket() string { return c.bucket }

// target resolves host and raw path for key under the addressing style.
func (c *Client) target(key string) (host, path string) {
	if c.virtualHosted {
		return c.bucket + "." + c.baseHost, "/" + key
	}
	return c.baseHost, "/" + c.bucket + "/" + key
}

// bucketTarget resolves host and raw path for bucket-level calls.
func (c *Client) bucketTarget() (host, path string) {
	if c.virtualHosted {
		return c.bucket + "." + c.baseHost, "/"
	}
	return c.baseHost, "/" + c.bucket
}

// PresignPut mints a short-lived PUT grant for key, which must belong to
// jobID's scope. A zero ttl selects the default.
func (c *Client) PresignPut(ctx context.Context, jobID, key string, ttl time.Duration) (Grant, error) {
	if err := ctx.Err(); err != nil {
		return Grant{}, fmt.Errorf("s3: presign PUT: %w", err)
	}
	if err := ValidateScopedKey(c.prefix, jobID, key); err != nil {
		return Grant{}, err
	}
	g, err := c.presign(http.MethodPut, key, nil, ttl)
	if err != nil {
		return Grant{}, fmt.Errorf("s3: presign PUT: %w", err)
	}
	return g, nil
}

// PresignGet mints a short-lived GET grant for key, which must belong to
// jobID's scope. A zero ttl selects the default.
func (c *Client) PresignGet(ctx context.Context, jobID, key string, ttl time.Duration) (Grant, error) {
	if err := ctx.Err(); err != nil {
		return Grant{}, fmt.Errorf("s3: presign GET: %w", err)
	}
	if err := ValidateScopedKey(c.prefix, jobID, key); err != nil {
		return Grant{}, err
	}
	g, err := c.presign(http.MethodGet, key, nil, ttl)
	if err != nil {
		return Grant{}, fmt.Errorf("s3: presign GET: %w", err)
	}
	return g, nil
}

// presignPutShaped mints a PUT grant for a job-scoped key without binding a
// specific job; used by the port-level Registry where the authorized job is
// unknown and the use-case layer enforces the binding instead.
func (c *Client) presignPutShaped(key string, ttl time.Duration) (Grant, error) {
	if err := ValidateJobScopedShape(c.prefix, key); err != nil {
		return Grant{}, err
	}
	return c.presign(http.MethodPut, key, nil, ttl)
}

// presignGetShaped is the GET counterpart of presignPutShaped.
func (c *Client) presignGetShaped(key string, ttl time.Duration) (Grant, error) {
	if err := ValidateJobScopedShape(c.prefix, key); err != nil {
		return Grant{}, err
	}
	return c.presign(http.MethodGet, key, nil, ttl)
}

// presign builds a SigV4 query-authenticated URL for method + key.
func (c *Client) presign(method, key string, sub map[string]string, ttl time.Duration) (Grant, error) {
	ttl, err := normalizeTTL(ttl, c.maxTTL)
	if err != nil {
		return Grant{}, err
	}
	now := c.now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	scope := dateStamp + "/" + c.region + "/" + s3Service + "/" + aws4Request

	host, rawPath := c.target(key)
	canonicalURI := encodePath(rawPath)

	params := make(url.Values, len(sub)+5)
	for k, v := range sub {
		params.Set(k, v)
	}
	params.Set("X-Amz-Algorithm", amzAlgorithm)
	params.Set("X-Amz-Credential", c.access+"/"+scope)
	params.Set("X-Amz-Date", amzDate)
	params.Set("X-Amz-Expires", strconv.FormatInt(int64(ttl/time.Second), 10))
	params.Set("X-Amz-SignedHeaders", "host")
	canonicalQS := canonicalQuery(params)

	canonicalRequest := strings.Join([]string{
		method, canonicalURI, canonicalQS, "host:" + host + "\n", "host", "UNSIGNED-PAYLOAD",
	}, "\n")
	stringToSign := amzAlgorithm + "\n" + amzDate + "\n" + scope + "\n" + sha256Hex(canonicalRequest)
	signature := hexEncode(hmacSHA256(deriveSigningKey(c.secret, dateStamp, c.region), stringToSign))

	rawURL := c.scheme + "://" + host + canonicalURI + "?" + canonicalQS + "&X-Amz-Signature=" + signature
	return Grant{
		Method:    method,
		URL:       rawURL,
		Headers:   map[string]string{},
		ExpiresAt: now.Add(ttl),
		Bucket:    c.bucket,
		ObjectKey: key,
	}, nil
}

// Head returns stored size/checksum metadata for key. A missing object
// reports Exists=false with no error.
func (c *Client) Head(ctx context.Context, key string) (domain.ObjectMeta, error) {
	host, rawPath := c.target(key)
	rawURL := c.scheme + "://" + host + encodePath(rawPath)
	resp, err := c.roundTrip(ctx, http.MethodHead, rawURL, nil, "")
	if err != nil {
		var se *statusError
		if errors.As(err, &se) && se.status == http.StatusNotFound {
			return domain.ObjectMeta{Exists: false}, nil
		}
		return domain.ObjectMeta{}, err
	}
	return domain.ObjectMeta{
		Size:     parseContentLength(resp.header.Get("Content-Length")),
		Checksum: checksumFromHeaders(resp.header),
		Exists:   true,
	}, nil
}

// Delete removes key; a missing object is success (idempotent prune).
func (c *Client) Delete(ctx context.Context, key string) error {
	host, rawPath := c.target(key)
	rawURL := c.scheme + "://" + host + encodePath(rawPath)
	_, err := c.roundTrip(ctx, http.MethodDelete, rawURL, nil, "")
	if err != nil {
		var se *statusError
		if errors.As(err, &se) && se.status == http.StatusNotFound {
			return nil
		}
		return err
	}
	return nil
}

// Ping tests destination connectivity and credentials with a HEAD Bucket
// call. The bucket name is safe to retain; credentials never are.
func (c *Client) Ping(ctx context.Context) error {
	host, rawPath := c.bucketTarget()
	rawURL := c.scheme + "://" + host + encodePath(rawPath)
	if _, err := c.roundTrip(ctx, http.MethodHead, rawURL, nil, ""); err != nil {
		return fmt.Errorf("s3: ping bucket %q: %w", c.bucket, err)
	}
	return nil
}

// signHeader applies SigV4 header authentication to a direct request.
func (c *Client) signHeader(req *http.Request, payloadHash string, now time.Time) {
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	scope := dateStamp + "/" + c.region + "/" + s3Service + "/" + aws4Request

	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)

	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := "host:" + host + "\n" +
		"x-amz-content-sha256:" + payloadHash + "\n" +
		"x-amz-date:" + amzDate + "\n"
	canonicalRequest := strings.Join([]string{
		req.Method, req.URL.EscapedPath(), canonicalQuery(req.URL.Query()),
		canonicalHeaders, signedHeaders, payloadHash,
	}, "\n")
	stringToSign := amzAlgorithm + "\n" + amzDate + "\n" + scope + "\n" + sha256Hex(canonicalRequest)
	signature := hexEncode(hmacSHA256(deriveSigningKey(c.secret, dateStamp, c.region), stringToSign))
	req.Header.Set("Authorization", amzAlgorithm+" Credential="+c.access+"/"+scope+
		", SignedHeaders="+signedHeaders+", Signature="+signature)
}

// response is a fully-read HTTP response.
type response struct {
	status int
	header http.Header
	body   []byte
}

// statusError is a non-retryable-by-default HTTP failure. url must already
// be redacted at construction.
type statusError struct {
	method string
	url    string
	status int
	body   string
}

func (e *statusError) Error() string {
	if e.body == "" {
		return fmt.Sprintf("s3: %s %s: unexpected status %d", e.method, e.url, e.status)
	}
	return fmt.Sprintf("s3: %s %s: unexpected status %d: %s", e.method, e.url, e.status, e.body)
}

// roundTrip executes one request with per-call timeout and retry with
// backoff on transient failures (network errors, 408, 429, 5xx).
func (c *Client) roundTrip(ctx context.Context, method, rawURL string, body []byte, contentType string) (response, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	payload := body
	if payload == nil {
		payload = []byte{}
	}
	op := func() (response, error) {
		req, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(payload))
		if err != nil {
			return response{}, backoff.Permanent(fmt.Errorf("s3: build request: %w", err))
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		c.signHeader(req, sha256HexBytes(payload), c.now().UTC())
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return response{}, err // network failure: retryable
		}
		defer func() { _ = resp.Body.Close() }()
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		header := resp.Header.Clone()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return response{status: resp.StatusCode, header: header, body: respBody}, nil
		}
		serr := &statusError{
			method: method,
			url:    RedactURL(rawURL),
			status: resp.StatusCode,
			body:   strings.TrimSpace(string(respBody)),
		}
		if resp.StatusCode == http.StatusTooManyRequests ||
			resp.StatusCode == http.StatusRequestTimeout ||
			resp.StatusCode >= 500 {
			if secs, ok := parseRetryAfter(resp.Header.Get("Retry-After")); ok {
				return response{}, errors.Join(serr, backoff.RetryAfter(secs))
			}
			return response{}, serr
		}
		return response{}, backoff.Permanent(serr)
	}
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = c.retryInitial
	return backoff.Retry(ctx, op,
		backoff.WithBackOff(bo),
		backoff.WithMaxTries(c.maxTries),
	)
}

// withTimeout applies the client default when the caller set no deadline.
func (c *Client) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.timeout)
}

// parseRetryAfter parses a delta-seconds Retry-After value.
func parseRetryAfter(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	secs, err := strconv.Atoi(value)
	if err != nil || secs < 0 {
		return 0, false
	}
	return secs, true
}

// parseContentLength parses a Content-Length header, leniently.
func parseContentLength(value string) uint64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// isIPHost reports whether hostport names a literal IP (with optional port).
func isIPHost(hostport string) bool {
	h := hostport
	if hh, _, err := net.SplitHostPort(hostport); err == nil {
		h = hh
	}
	h = strings.Trim(h, "[]")
	return net.ParseIP(h) != nil
}

// dnsCompatibleBucket reports whether bucket can be a DNS label for
// virtual-hosted style addressing.
func dnsCompatibleBucket(bucket string) bool {
	if len(bucket) < 3 || len(bucket) > 63 {
		return false
	}
	for i := 0; i < len(bucket); i++ {
		ch := bucket[i]
		if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '.' {
			continue
		}
		return false
	}
	if bucket[0] == '.' || bucket[0] == '-' || bucket[len(bucket)-1] == '.' || bucket[len(bucket)-1] == '-' {
		return false
	}
	if strings.Contains(bucket, "..") {
		return false
	}
	return net.ParseIP(bucket) == nil
}
