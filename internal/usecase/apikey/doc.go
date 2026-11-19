// Package apikey issues and verifies user API keys for programmatic
// (CLI) access (PRD §7.1). Secrets (`vxk_...`) are returned exactly once
// at issuance; only SHA-256 hashes are persisted via domain ports.
package apikey
