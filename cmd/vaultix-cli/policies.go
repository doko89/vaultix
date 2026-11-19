package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

// policyJSON mirrors the policies policyDTO shape (displayed fields only).
type policyJSON struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Description    string    `json:"description"`
	AgentIDs       []string  `json:"agent_ids"`
	StorageID      string    `json:"storage_id"`
	Enabled        bool      `json:"enabled"`
	CurrentVersion int       `json:"current_version"`
	CreatedBy      string    `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	Compression    struct {
		Algorithm string `json:"algorithm"`
		Level     int    `json:"level"`
	} `json:"compression"`
	Encryption struct {
		Enabled bool   `json:"enabled"`
		KeyRef  string `json:"key_ref"`
	} `json:"encryption"`
}

type policyListJSON struct {
	Items []policyJSON `json:"items"`
	Total int          `json:"total"`
}

func (a *app) newPoliciesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policies",
		Short: "Inspect backup policies",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List backup policies",
			RunE: func(cmd *cobra.Command, _ []string) error {
				raw, err := a.call(cmd, "policies.list", nil, nil, nil)
				if err != nil {
					return err
				}
				return a.emit(cmd, raw, func() error {
					var list policyListJSON
					if err := decode(raw, "policies.list", &list); err != nil {
						return err
					}
					if len(list.Items) == 0 {
						cmd.Println("No policies found.")
						return nil
					}
					rows := make([][]string, 0, len(list.Items))
					for _, p := range list.Items {
						rows = append(rows, []string{
							p.ID, p.Name, fmtBool(p.Enabled),
							fmt.Sprintf("%d", p.CurrentVersion),
							fmt.Sprintf("%d", len(p.AgentIDs)),
							orDash(p.StorageID),
						})
					}
					writeTable(cmd.OutOrStdout(),
						[]string{"ID", "NAME", "ENABLED", "VERSION", "AGENTS", "STORAGE"}, rows)
					return nil
				})
			},
		},
		&cobra.Command{
			Use:   "show <id>",
			Short: "Show one policy",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				raw, err := a.call(cmd, "policies.show", map[string]string{"id": args[0]}, nil, nil)
				if err != nil {
					return err
				}
				return a.emit(cmd, raw, func() error {
					var p policyJSON
					if err := decode(raw, "policies.show", &p); err != nil {
						return err
					}
					enc := "disabled"
					if p.Encryption.Enabled {
						enc = "enabled (key " + orDash(p.Encryption.KeyRef) + ")"
					}
					writeKV(cmd.OutOrStdout(), [][2]string{
						{"ID", p.ID},
						{"Name", p.Name},
						{"Description", orDash(p.Description)},
						{"Enabled", fmtBool(p.Enabled)},
						{"Version", fmt.Sprintf("%d", p.CurrentVersion)},
						{"Agents", fmtList(p.AgentIDs)},
						{"Storage", orDash(p.StorageID)},
						{"Compression", p.Compression.Algorithm},
						{"Encryption", enc},
						{"Created by", orDash(p.CreatedBy)},
						{"Created", p.CreatedAt.UTC().Format(time.RFC3339)},
						{"Updated", p.UpdatedAt.UTC().Format(time.RFC3339)},
					})
					return nil
				})
			},
		},
	)
	return cmd
}
