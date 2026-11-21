//go:build !unix

package agent

import "os/exec"

// armGroupKill falls back to killing the direct child: Windows has no
// process-group kill. Grandchild orphans are possible on cancellation.
func armGroupKill(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
}
