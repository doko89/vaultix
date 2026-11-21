package ghrelease

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLatestReleaseParses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/releases/latest" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v2.0.0","assets":[
			{"name":"vaultix-agent-linux-amd64","browser_download_url":"http://x/amd64"},
			{"name":"","browser_download_url":"http://x/empty"},
			{"name":"noname","browser_download_url":""}
		]}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "o/r")
	rel, err := c.LatestRelease(context.Background())
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if rel.Tag != "v2.0.0" || len(rel.Assets) != 1 || rel.Assets[0].Name != "vaultix-agent-linux-amd64" {
		t.Fatalf("release: %+v", rel)
	}
}

func TestLatestReleaseRetriesThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"v3.0.0","assets":[]}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "o/r")
	rel, err := c.LatestRelease(context.Background())
	if err != nil {
		t.Fatalf("latest: %v", err)
	}
	if rel.Tag != "v3.0.0" || calls.Load() != 3 {
		t.Fatalf("release: %+v calls=%d", rel, calls.Load())
	}
}

func TestLatestRelease404NoRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "o/r")
	if _, err := c.LatestRelease(context.Background()); err == nil {
		t.Fatal("want error")
	}
	if calls.Load() != 1 {
		t.Fatalf("4xx must not retry: calls=%d", calls.Load())
	}
}

func TestDownloadCapsBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("0123456789"))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "o/r")
	data, err := c.Download(context.Background(), srv.URL+"/f", 64)
	if err != nil || string(data) != "0123456789" {
		t.Fatalf("download: %q %v", data, err)
	}
	if _, err := c.Download(context.Background(), srv.URL+"/f", 4); err == nil {
		t.Fatal("want cap error")
	}
	if _, err := c.Download(context.Background(), "  ", 64); err == nil {
		t.Fatal("want empty-url error")
	}
}

func TestLatestReleaseEmptyTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"  ","assets":[]}`))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "o/r")
	if _, err := c.LatestRelease(context.Background()); err == nil || !strings.Contains(err.Error(), "no tag") {
		t.Fatalf("want no-tag error, got %v", err)
	}
}
