//go:build unix

package agent

import "syscall"

// procSelfExec replaces the process image (same PID: systemd keeps
// supervising the new binary with no unit changes).
func procSelfExec(argv0 string, argv, env []string) error {
	return syscall.Exec(argv0, argv, env)
}
