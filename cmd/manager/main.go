// Command manager is the Vaultix Manager entrypoint (skeleton).
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var version = "0.0.0-dev"

func main() {
	root := &cobra.Command{
		Use:   "manager",
		Short: "Vaultix Manager: centralized backup control plane",
	}
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Println("manager", version)
		},
	})
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
