package agent

import (
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

// CompareVersions compares dotted version strings (shared domain ordering;
// see domain.CompareVersions).
func CompareVersions(a, b string) int {
	return domain.CompareVersions(a, b)
}
