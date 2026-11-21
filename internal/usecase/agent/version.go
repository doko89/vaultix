package agent

import (
	"context"
	"strings"

	"github.com/vaultix/vaultix/internal/domain"
)

// IsOutdated reports whether version is older than the service minimum. An
// empty minimum disables the flag; an empty agent version is outdated when a
// minimum is configured.
func (s *Service) IsOutdated(version string) bool {
	if s.minVersion == "" {
		return false
	}
	return CompareVersions(version, s.minVersion) < 0
}

// outdatedFor flags agents older than the newest cached binary for their
// platform (unknown platforms compare against every cached build).
// Revoked agents never flag (they cannot update); without an update repo,
// with no cached builds, or on lookup failure it falls back to the
// minVersion comparison.
func (s *Service) outdatedFor(ctx context.Context, ag *domain.Agent) bool {
	if ag.Disabled || s.updates == nil {
		return !ag.Disabled && s.IsOutdated(ag.Version)
	}
	arts, err := s.updates.ListArtifacts(ctx)
	if err != nil {
		return s.IsOutdated(ag.Version)
	}
	return s.outdatedAgainst(arts, ag)
}

// outdatedAgainst is outdatedFor over prefetched artifacts (listings fetch
// once instead of per row).
func (s *Service) outdatedAgainst(arts []domain.AgentArtifact, ag *domain.Agent) bool {
	if ag.Disabled {
		return false
	}
	var newest *domain.AgentArtifact
	for i := range arts {
		a := &arts[i]
		if ag.OS != "" && !strings.EqualFold(a.GOOS, ag.OS) {
			continue
		}
		if ag.Architecture != "" && !strings.EqualFold(a.GOARCH, ag.Architecture) {
			continue
		}
		if newest == nil || domain.CompareVersions(newest.Version, a.Version) < 0 {
			newest = a
		}
	}
	if newest == nil {
		return s.IsOutdated(ag.Version)
	}
	return domain.CompareVersions(ag.Version, newest.Version) < 0
}

// CompareVersions compares dotted version strings (shared domain ordering;
// see domain.CompareVersions).
func CompareVersions(a, b string) int {
	return domain.CompareVersions(a, b)
}
