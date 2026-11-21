//go:build !unix

package agent

import "errors"

// diskUsage is unsupported off unix: heartbeats omit disk facts there.
func diskUsage(_ string) (uint64, uint64, error) {
	return 0, 0, errors.New("agent: disk usage unsupported on this platform")
}
