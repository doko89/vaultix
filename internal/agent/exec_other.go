//go:build !unix

package agent

import "errors"

// procSelfExec is unsupported off unix: Apply restores the backup and
// reports failed there (release builds are unix-only).
func procSelfExec(_ string, _, _ []string) error {
	return errors.New("agent: self-exec unsupported on this platform")
}
