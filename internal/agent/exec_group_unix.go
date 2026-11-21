//go:build unix

package agent

import (
	"os/exec"
	"syscall"
)

// armGroupKill makes CommandContext cancellation kill the whole process group.
// Without it, killing only the direct child orphans grandchildren (shell
// wrappers, tool subprocesses) that keep the stdio pipes open, and cmd.Wait
// blocks forever waiting for EOF.
func armGroupKill(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative pid = process group (the child's own, via Setpgid).
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return cmd.Process.Kill()
	}
}
