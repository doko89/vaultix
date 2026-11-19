package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientTimeoutIs15s(t *testing.T) {
	c := NewClient("https://manager:8443", "vxk_x", false, nil)
	if c.httpClient.Timeout != 15*time.Second {
		t.Fatalf("timeout = %v, want 15s", c.httpClient.Timeout)
	}
}

func TestClientSendsBearerAuth(t *testing.T) {
	var gotAuth, gotCT, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotCT, gotAccept = r.Header.Get("Authorization"), r.Header.Get("Content-Type"), r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "vxk_secret_key", false, nil)
	raw, err := c.Do(context.Background(), "POST", "/api/jobs/", nil, map[string]string{"a": "b"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if string(raw) != `{"ok":true}` {
		t.Fatalf("raw body = %s", raw)
	}
	if gotAuth != "Bearer vxk_secret_key" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotCT != "application/json" {
		t.Fatalf("Content-Type = %q", gotCT)
	}
	if gotAccept != "application/json" {
		t.Fatalf("Accept = %q", gotAccept)
	}
}

func TestClientRetriesOnly502503504(t *testing.T) {
	// Each transient status costs one retry and then succeeds.
	for _, code := range []int{502, 503, 504} {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 1 {
				w.WriteHeader(code)
				_, _ = w.Write([]byte(`{}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		c := NewClient(srv.URL, "vxk_x", false, nil)
		if _, err := c.Do(context.Background(), "GET", "/api/agents", nil, nil); err != nil {
			srv.Close()
			t.Fatalf("status %d: expected success after retry: %v", code, err)
		}
		srv.Close()
		if calls != 2 {
			t.Fatalf("status %d: calls = %d, want 2 (one retry)", code, calls)
		}
	}
}

// TestClientNoRetryOn4xx pins the never-retry set: 400/401/404/409/422 must
// each cost exactly one attempt.
func TestClientNoRetryOn4xx(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404, 409, 422} {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"error":{"code":"x","message":"nope"}}`))
		}))
		c := NewClient(srv.URL, "vxk_x", false, nil)
		_, err := c.Do(context.Background(), "GET", "/api/agents", nil, nil)
		srv.Close()
		if err == nil {
			t.Fatalf("status %d: expected error", code)
		}
		if calls != 1 {
			t.Fatalf("status %d: calls = %d, want exactly 1", code, calls)
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) || apiErr.Status != code {
			t.Fatalf("status %d: err = %v, want *APIError", code, err)
		}
	}
}

func TestClientNoRetryOn500(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`boom`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "vxk_x", false, nil)
	_, err := c.Do(context.Background(), "GET", "/api/agents", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want exactly 1 (no retry on 500)", calls)
	}
}

func TestClientRetriesExhaustedReportsAPIError(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":503,"message":"down for maintenance"}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "vxk_x", false, nil)
	_, err := c.Do(context.Background(), "GET", "/api/agents", nil, nil)
	if calls != maxTries {
		t.Fatalf("calls = %d, want maxTries=%d", calls, maxTries)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *APIError", err, err)
	}
	if apiErr.Status != 503 || apiErr.Message != "down for maintenance" {
		t.Fatalf("apiErr = %+v", apiErr)
	}
}

func TestClientParsesBothEnvelopes(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"root int-code envelope", `{"error":{"code":404,"message":"agent not found","request_id":"r1"}}`, "agent not found"},
		{"jobs string-code envelope", `{"error":{"code":"not_found","message":"job ghost missing"}}`, "job ghost missing"},
		{"plain body fallback", `plain failure`, "plain failure"},
		{"empty body falls back to status text", ``, "Not Found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			c := NewClient(srv.URL, "vxk_x", false, nil)
			_, err := c.Do(context.Background(), "GET", "/api/agents/x", nil, nil)
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v, want *APIError", err)
			}
			if apiErr.Message != tc.want {
				t.Fatalf("message = %q, want %q", apiErr.Message, tc.want)
			}
		})
	}
}

func TestClient401HintsLogin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":401,"message":"bad key"}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "vxk_stale", false, nil)
	_, err := c.Do(context.Background(), "GET", "/api/agents", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "vaultix-cli login") {
		t.Fatalf("401 error should hint at login, got %v", err)
	}
}

func TestClientDebugRedactsSecrets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Even a hostile body echoing the key must never reach --debug.
		_, _ = w.Write([]byte(`{"echo":"` + r.Header.Get("Authorization") + `"}`))
	}))
	defer srv.Close()

	var debug bytes.Buffer
	c := NewClient(srv.URL, "vxk_super_secret_key", true, &debug)
	if _, err := c.Do(context.Background(), "GET", "/api/auth/me", nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	out := debug.String()
	if !strings.Contains(out, "GET") || !strings.Contains(out, "/api/auth/me") || !strings.Contains(out, "200") {
		t.Fatalf("debug line should carry method+URL+status, got %q", out)
	}
	if strings.Contains(out, "vxk_") {
		t.Fatalf("debug output leaks the key: %q", out)
	}
	if strings.Contains(out, "Authorization") {
		t.Fatalf("debug output must never mention headers: %q", out)
	}
}

func TestClientDebugOffIsSilent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	var debug bytes.Buffer
	c := NewClient(srv.URL, "vxk_x", false, &debug)
	if _, err := c.Do(context.Background(), "GET", "/x", nil, nil); err != nil {
		t.Fatal(err)
	}
	if debug.Len() != 0 {
		t.Fatalf("debug off should be silent, got %q", debug.String())
	}
}
