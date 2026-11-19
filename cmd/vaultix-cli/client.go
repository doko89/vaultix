package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v5"
)

// clientTimeout bounds every Manager request, including dial and body.
const clientTimeout = 15 * time.Second

// maxTries bounds the backoff loop: one attempt plus retries on 502/503/504.
const maxTries = 3

// APIError is a Manager-side failure: the HTTP status plus the message from
// the error envelope (both envelope shapes — the root {error:{code:int,
// message}} and the jobs {error:{code:string, message}} — parse into it).
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	out := fmt.Sprintf("manager error %d: %s", e.Status, msg)
	if e.Status == http.StatusUnauthorized {
		out += " (hint: run `vaultix-cli login` to re-authenticate)"
	}
	return out
}

// Client is the thin Manager HTTP layer: base URL + Bearer auth, a 15s
// timeout, backoff retries on 502/503/504 only, and envelope parsing. It
// never retries 4xx (or 500/501/505+): those are final answers.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	debug      bool
	debugW     io.Writer
}

// NewClient builds a Client for baseURL authenticated with apiKey. When
// debug is set, each call logs one method+URL+status line to debugW — never
// headers or bodies, so secrets cannot leak through --debug.
func NewClient(baseURL, apiKey string, debug bool, debugW io.Writer) *Client {
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: clientTimeout},
		debug:      debug,
		debugW:     debugW,
	}
}

// Do performs method+path (path may be absolute-path only) with an optional
// query and an optional JSON body, returning the raw response body for the
// caller to decode (or to print verbatim in --output json).
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body any) (json.RawMessage, error) {
	fullURL := c.baseURL + path
	if len(query) > 0 {
		fullURL += "?" + query.Encode()
	}
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request body: %w", err)
		}
	}

	var lastStatus int
	var lastBody []byte
	var transportErr error
	op := func() (int, error) {
		status, respBody, err := c.doOnce(ctx, method, fullURL, bodyBytes)
		if err != nil {
			transportErr = err
			return 0, backoff.Permanent(err)
		}
		lastStatus, lastBody = status, respBody
		if status == http.StatusBadGateway ||
			status == http.StatusServiceUnavailable ||
			status == http.StatusGatewayTimeout {
			return 0, fmt.Errorf("transient manager status %d", status)
		}
		return status, nil
	}
	if _, err := backoff.Retry(ctx, op, backoff.WithMaxTries(uint(maxTries))); err != nil {
		if transportErr != nil {
			return nil, transportErr
		}
		// Retries exhausted: fall through and report the last 502/503/504
		// as a regular APIError instead of a backoff error.
	}
	c.debugLog(method, fullURL, lastStatus)

	if lastStatus < 200 || lastStatus >= 300 {
		return nil, &APIError{Status: lastStatus, Message: parseErrorMessage(lastStatus, lastBody)}
	}
	raw := json.RawMessage(bytes.TrimSpace(lastBody))
	if len(raw) == 0 {
		return nil, nil
	}
	return raw, nil
}

// doOnce executes a single HTTP attempt.
func (c *Client) doOnce(ctx context.Context, method, fullURL string, bodyBytes []byte) (int, []byte, error) {
	var reader io.Reader
	if bodyBytes != nil {
		reader = bytes.NewReader(bodyBytes)
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if bodyBytes != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("request %s %s: %w", method, fullURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("read response: %w", err)
	}
	return resp.StatusCode, respBody, nil
}

// debugLog writes the single --debug line for a call: method, URL, status.
// Headers (Authorization) and bodies (keys, tokens) are never logged.
func (c *Client) debugLog(method, fullURL string, status int) {
	if !c.debug || c.debugW == nil {
		return
	}
	fmt.Fprintf(c.debugW, "debug: %s %s -> %d\n", method, fullURL, status)
}

// parseErrorMessage extracts the human message from either Manager error
// envelope, falling back to the raw body (truncated) or the status text.
func parseErrorMessage(status int, body []byte) string {
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err == nil && env.Error.Message != "" {
		return env.Error.Message
	}
	if trimmed := strings.TrimSpace(string(body)); trimmed != "" {
		if len(trimmed) > 300 {
			trimmed = trimmed[:300] + "…"
		}
		return trimmed
	}
	if text := http.StatusText(status); text != "" {
		return text
	}
	return "request failed"
}
