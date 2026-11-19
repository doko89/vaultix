package main

import (
	"github.com/spf13/cobra"
)

func newRootCommand() *cobra.Command {
	var configPath string
	root := &cobra.Command{
		Use:           "manager",
		Short:         "Vaultix Manager: centralized backup control plane",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&configPath, "config", "", "config file path (YAML; default ./manager.yaml, /etc/vaultix/manager.yaml)")
	root.AddCommand(newServeCommand(&configPath), newMigrateCommand(&configPath), newSeedAdminCommand(&configPath), newVersionCommand())
	return root
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print manager version",
		Run: func(cmd *cobra.Command, _ []string) {
			cmd.Println("manager", version)
		},
	}
}
