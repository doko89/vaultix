//go:build !windows

package main

// maybeRunService is a no-op off Windows: the agent always runs as a CLI.
func maybeRunService() bool { return false }
