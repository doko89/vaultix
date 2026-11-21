//go:build !unix

package agent

import "errors"

// procSelfExec is unsupported off unix: Apply restores the backup and
// reports failed there, so Windows agents update by manual binary swap.
func procSelfExec(_ string, _, _ []string) error {
	return errors.New("agent: self-update is not supported on this platform; replace the binary manually and restart the service")
}
