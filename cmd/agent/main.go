// Command agent is the Vaultix Agent entrypoint: enroll, run, version.
package main

import (
	"os"
)

// version is overridden at release build time:
//
//	go build -ldflags "-X main.version=1.2.3" ./cmd/agent
var version = "0.0.0-dev"

func main() {
	if err := newRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}
