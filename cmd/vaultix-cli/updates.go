package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

type artifactListJSON struct {
	Items []artifactJSON `json:"items"`
}

type checkResultJSON struct {
	CheckedAt string         `json:"checked_at"`
	Cached    []artifactJSON `json:"cached"`
	UpToDate  bool           `json:"up_to_date"`
}

func (a *app) newUpdatesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "updates",
		Short: "Manage cached agent update binaries",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "check",
			Short: "Sync the latest release binaries into the cache",
			RunE: func(cmd *cobra.Command, _ []string) error {
				raw, err := a.call(cmd, "updates.check", nil, nil, nil)
				if err != nil {
					return err
				}
				return a.emit(cmd, raw, func() error {
					var res checkResultJSON
					if err := decode(raw, "updates.check", &res); err != nil {
						return err
					}
					if res.UpToDate {
						cmd.Println("Agent binaries are up to date.")
						return nil
					}
					for _, art := range res.Cached {
						cmd.Printf("Cached %s (%s/%s)\n", art.Version, art.GOOS, art.GOARCH)
					}
					return nil
				})
			},
		},
		&cobra.Command{
			Use:   "list",
			Short: "List cached agent binaries",
			RunE: func(cmd *cobra.Command, _ []string) error {
				raw, err := a.call(cmd, "updates.list", nil, nil, nil)
				if err != nil {
					return err
				}
				return a.emit(cmd, raw, func() error {
					var list artifactListJSON
					if err := decode(raw, "updates.list", &list); err != nil {
						return err
					}
					if len(list.Items) == 0 {
						cmd.Println("No cached binaries.")
						return nil
					}
					rows := make([][]string, 0, len(list.Items))
					for _, art := range list.Items {
						rows = append(rows, []string{
							art.ID, art.Version, art.GOOS + "/" + art.GOARCH,
							fmt.Sprintf("%d", art.SizeBytes), art.CreatedAt,
						})
					}
					writeTable(cmd.OutOrStdout(),
						[]string{"ID", "VERSION", "PLATFORM", "SIZE", "CACHED_AT"}, rows)
					return nil
				})
			},
		},
		&cobra.Command{
			Use:   "delete <id>",
			Short: "Delete a cached binary",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				raw, err := a.call(cmd, "updates.delete", map[string]string{"id": args[0]}, nil, nil)
				if err != nil {
					return err
				}
				return a.emit(cmd, raw, func() error {
					cmd.Printf("Deleted cached binary %s\n", args[0])
					return nil
				})
			},
		},
	)
	return cmd
}
