// Command agent is the Vaultix Agent entrypoint (skeleton).
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "0.0.0-dev"

func main() {
	root := &cobra.Command{
		Use:   "agent",
		Short: "Vaultix Agent: per-VM backup executor",
	}
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Println("agent", version)
		},
	})
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
