// Package agent holds the Vaultix agent runtime: job polling, backup
// execution (filesystem, docker volumes, postgres, mysql), chunked
// envelope encryption, presigned-URL upload (single + multipart), restore
// execution, heartbeat, discovery, and crash-safe local state.
package agent
