// Command manager is the Vaultix Manager entrypoint: API + scheduler +
// embedded web UI (control plane).
package main

import (
	"fmt"
	"os"
)

var version = "0.0.0-dev"

func main() {
	if err := newRootCommand().Execute(); err != nil {
		// Cobra silences usage/errors (SilenceUsage/SilenceErrors), so the
		// error is printed here exactly once.
		fmt.Fprintln(os.Stderr, "manager:", err)
		os.Exit(1)
	}
}
