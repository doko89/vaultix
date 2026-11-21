package agentupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// ReleaseSource lists and downloads release files (GitHub releases in
// production, fakes in tests).
type ReleaseSource interface {
	LatestRelease(ctx context.Context) (Release, error)
	Download(ctx context.Context, url string, maxBytes int64) ([]byte, error)
}

// Release is one upstream release: version tag plus files.
type Release struct {
	Tag    string
	Assets []ReleaseAsset
}

// ReleaseAsset is one named downloadable file.
type ReleaseAsset struct {
	Name string
	URL  string
}

// wantedBinaries maps release asset names to platforms. sha256sumsFile
// carries the checksums release.yml publishes next to the binaries.
// wantedBinaries maps exact release asset names to platforms. Windows
// assets carry .exe so they run on download.
var wantedBinaries = map[string][2]string{
	"vaultix-agent-linux-amd64":       {"linux", "amd64"},
	"vaultix-agent-linux-arm64":       {"linux", "arm64"},
	"vaultix-agent-darwin-amd64":      {"darwin", "amd64"},
	"vaultix-agent-darwin-arm64":      {"darwin", "arm64"},
	"vaultix-agent-windows-amd64.exe": {"windows", "amd64"},
	"vaultix-agent-windows-arm64.exe": {"windows", "arm64"},
}

const sha256sumsFile = "sha256sums.txt"

// SyncStatus is one release-sync outcome.
type SyncStatus struct {
	At     time.Time
	Cached []domain.AgentArtifact
	Error  string
}

// WithReleaseSource wires the upstream release fetcher (nil disables
// SyncReleases).
func WithReleaseSource(src ReleaseSource) func(*Service) {
	return func(s *Service) { s.source = src }
}

// SyncReleases caches the latest release's agent binaries that are not
// cached yet, verifying each against the release's sha256sums.txt.
func (s *Service) SyncReleases(ctx context.Context) (*SyncStatus, error) {
	out := &SyncStatus{At: time.Now().UTC()}
	if s.source == nil {
		out.Error = "release source is not configured"
		return out, fmt.Errorf("%w: %s", domain.ErrValidation, out.Error)
	}
	rel, err := s.source.LatestRelease(ctx)
	if err != nil {
		out.Error = err.Error()
		return out, fmt.Errorf("agentupdate: latest release: %w", err)
	}
	version := strings.TrimSpace(rel.Tag)
	if version == "" {
		out.Error = "latest release has no tag"
		return out, fmt.Errorf("%w: %s", domain.ErrValidation, out.Error)
	}
	byName := map[string]string{}
	for _, a := range rel.Assets {
		byName[a.Name] = a.URL
	}
	type missing struct {
		name   string
		goos   string
		goarch string
		url    string
	}
	var todo []missing
	for name, plat := range wantedBinaries {
		url, ok := byName[name]
		if !ok {
			continue
		}
		found, err := s.findArtifact(ctx, version, plat[0], plat[1])
		if err != nil {
			out.Error = err.Error()
			return out, err
		}
		if found == nil {
			todo = append(todo, missing{name: name, goos: plat[0], goarch: plat[1], url: url})
		}
	}
	if len(todo) == 0 {
		return out, nil
	}
	sumsURL, ok := byName[sha256sumsFile]
	if !ok {
		out.Error = fmt.Sprintf("release %s has no %s", version, sha256sumsFile)
		return out, fmt.Errorf("%w: %s", domain.ErrValidation, out.Error)
	}
	sumsRaw, err := s.source.Download(ctx, sumsURL, 1<<20)
	if err != nil {
		out.Error = err.Error()
		return out, fmt.Errorf("agentupdate: download %s: %w", sha256sumsFile, err)
	}
	sums, err := parseSums(sumsRaw)
	if err != nil {
		out.Error = err.Error()
		return out, fmt.Errorf("%w: %s", domain.ErrValidation, out.Error)
	}
	for _, m := range todo {
		want, ok := sums[m.name]
		if !ok || len(want) == 0 {
			out.Error = fmt.Sprintf("release %s: no checksum for %s", version, m.name)
			return out, fmt.Errorf("%w: %s", domain.ErrValidation, out.Error)
		}
		data, err := s.source.Download(ctx, m.url, MaxArtifactBytes)
		if err != nil {
			out.Error = err.Error()
			return out, fmt.Errorf("agentupdate: download %s: %w", m.name, err)
		}
		sum := sha256.Sum256(data)
		if !equalBytes(sum[:], want) {
			out.Error = fmt.Sprintf("release %s: %s checksum mismatch", version, m.name)
			return out, fmt.Errorf("%w: %s", domain.ErrValidation, out.Error)
		}
		art, err := s.UploadArtifact(ctx, version, m.goos, m.goarch, data, "release-sync")
		if err != nil {
			out.Error = err.Error()
			return out, err
		}
		out.Cached = append(out.Cached, *art)
	}
	return out, nil
}

// parseSums parses sha256sum output ("<hex>[ *]<file>" per line).
func parseSums(raw []byte) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		hexSum, name, ok := strings.Cut(line, " ")
		if !ok {
			return nil, fmt.Errorf("malformed sha256sums line %q", line)
		}
		name = strings.TrimLeft(strings.TrimSpace(name), "*")
		sum, err := hex.DecodeString(strings.TrimSpace(hexSum))
		if err != nil || len(sum) != sha256.Size {
			return nil, fmt.Errorf("malformed sha256sums line %q", line)
		}
		out[name] = sum
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("sha256sums is empty")
	}
	return out, nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
