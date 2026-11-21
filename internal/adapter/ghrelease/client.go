// Package ghrelease syncs agent binaries from GitHub releases: it reads
// the latest release of a repo and downloads named assets. Every call
// carries a timeout and retries transient failures; callers additionally
// bound calls with ctx.
package ghrelease

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	updateuc "github.com/vaultix/vaultix/internal/usecase/agentupdate"
)

const (
	requestTimeout = 30 * time.Second
	maxAttempts    = 3
	retryWait      = 2 * time.Second
)

// Client fetches releases from a GitHub-compatible API base.
type Client struct {
	api  string
	repo string
	http *http.Client
}

// NewClient builds a fetcher for repo ("owner/name") under apiBase
// (https://api.github.com; tests point it at a fake server).
func NewClient(apiBase, repo string) *Client {
	return &Client{
		api:  strings.TrimSuffix(strings.TrimSpace(apiBase), "/"),
		repo: strings.TrimSpace(repo),
		http: &http.Client{Timeout: requestTimeout},
	}
}

type releaseJSON struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// LatestRelease returns the repo's latest release.
func (c *Client) LatestRelease(ctx context.Context) (updateuc.Release, error) {
	var out updateuc.Release
	body, err := c.get(ctx, c.api+"/repos/"+c.repo+"/releases/latest", 1<<20, "application/json")
	if err != nil {
		return out, err
	}
	var raw releaseJSON
	if err := json.Unmarshal(body, &raw); err != nil {
		return out, fmt.Errorf("ghrelease: decode latest release: %w", err)
	}
	if strings.TrimSpace(raw.TagName) == "" {
		return out, fmt.Errorf("ghrelease: latest release has no tag")
	}
	out.Tag = strings.TrimSpace(raw.TagName)
	for _, a := range raw.Assets {
		if a.Name == "" || a.BrowserDownloadURL == "" {
			continue
		}
		out.Assets = append(out.Assets, updateuc.ReleaseAsset{Name: a.Name, URL: a.BrowserDownloadURL})
	}
	return out, nil
}

// Download fetches one asset URL, capped at maxBytes.
func (c *Client) Download(ctx context.Context, url string, maxBytes int64) ([]byte, error) {
	if strings.TrimSpace(url) == "" {
		return nil, fmt.Errorf("ghrelease: empty download url")
	}
	if maxBytes <= 0 {
		return nil, fmt.Errorf("ghrelease: maxBytes must be positive")
	}
	return c.get(ctx, url, maxBytes, "application/octet-stream")
}

// get performs a capped GET with timeout + retries on transient failures.
func (c *Client) get(ctx context.Context, url string, maxBytes int64, accept string) ([]byte, error) {
	var last error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		data, retry, err := c.getOnce(ctx, url, maxBytes, accept)
		if err == nil {
			return data, nil
		}
		last = err
		if !retry || attempt == maxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("ghrelease: GET %s: %w", url, ctx.Err())
		case <-time.After(retryWait * time.Duration(attempt)):
		}
	}
	return nil, last
}

func (c *Client) getOnce(ctx context.Context, url string, maxBytes int64, accept string) (data []byte, retry bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, fmt.Errorf("ghrelease: build request: %w", err)
	}
	req.Header.Set("Accept", accept)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, true, fmt.Errorf("ghrelease: GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return nil, true, fmt.Errorf("ghrelease: GET %s: status %s", url, resp.Status)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, false, fmt.Errorf("ghrelease: GET %s: status %s: %s",
			url, resp.Status, strings.TrimSpace(string(detail)))
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, true, fmt.Errorf("ghrelease: read %s: %w", url, err)
	}
	if int64(len(data)) > maxBytes {
		return nil, false, fmt.Errorf("ghrelease: %s exceeds %d bytes", url, maxBytes)
	}
	return data, false, nil
}
