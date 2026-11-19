// Command vaultix-cli is the Vaultix Manager command-line client: it talks
// to the Manager's /api surface with a user API key (vxk_...) stored in
// ~/.config/vaultix/cli.yaml.
package main

import (
	"io"
	"os"
	"strings"
)

// version is overridden at release build time:
//
//	go build -ldflags "-X main.version=1.2.3" ./cmd/vaultix-cli
var version = "0.0.0-dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run executes the CLI with the given args and streams, returning the
// process exit code. Unknown commands/flags exit non-zero with usage;
// runtime failures (API errors, missing config) exit non-zero with an
// actionable message and no usage dump.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	root := newRootCommand()
	root.SetArgs(args)
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)
	if err := root.Execute(); err != nil {
		if isUsageError(err) {
			_, _ = io.WriteString(stderr, "Error: "+err.Error()+"\n")
			root.SetOut(stderr) // usage belongs with the error, not stdout
			_ = root.Usage()
		} else {
			_, _ = io.WriteString(stderr, "Error: "+err.Error()+"\n")
		}
		return 1
	}
	return 0
}

// isUsageError reports whether err came from command-line parsing (unknown
// command or flag) rather than from running a command.
func isUsageError(err error) bool {
	msg := err.Error()
	for _, sub := range []string{
		"unknown command",
		"unknown flag",
		"no such flag",
		"flag needs an argument",
	} {
		if strings.Contains(msg, sub) {
			return true
		}
	}
	return false
}
