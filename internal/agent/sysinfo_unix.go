//go:build unix

package agent

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// diskUsage returns total and free bytes for the filesystem holding path.
func diskUsage(path string) (total, free uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, fmt.Errorf("agent: statfs %s: %w", path, err)
	}
	bsize := uint64(st.Bsize)
	return st.Blocks * bsize, st.Bfree * bsize, nil
}
