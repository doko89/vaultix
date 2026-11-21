package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

// agentJSON mirrors the agents listAgentsResponse/agentSummary shape (only
// the fields this CLI displays; unknown fields are ignored).
type agentJSON struct {
	ID              string     `json:"id"`
	Hostname        string     `json:"hostname"`
	IPAddress       string     `json:"ip_address"`
	OS              string     `json:"os"`
	Architecture    string     `json:"architecture"`
	Version         string     `json:"version"`
	Capabilities    []string   `json:"capabilities"`
	Disabled        bool       `json:"disabled"`
	Status          string     `json:"status"`
	Outdated        bool       `json:"outdated"`
	LastHeartbeatAt *time.Time `json:"last_heartbeat_at"`
	EnrolledAt      time.Time  `json:"enrolled_at"`
}

type agentListJSON struct {
	Items []agentJSON `json:"items"`
	Total int         `json:"total"`
}

type updateSlotJSON struct {
	TargetVersion string `json:"target_version"`
	GOOS          string `json:"goos"`
	GOARCH        string `json:"goarch"`
	Status        string `json:"status"`
	Error         string `json:"error"`
	RequestedBy   string `json:"requested_by"`
	RequestedAt   string `json:"requested_at"`
	UpdatedAt     string `json:"updated_at"`
}

type artifactJSON struct {
	ID         string `json:"id"`
	Version    string `json:"version"`
	GOOS       string `json:"goos"`
	GOARCH     string `json:"goarch"`
	SHA256     string `json:"sha256"`
	SizeBytes  int64  `json:"size_bytes"`
	UploadedBy string `json:"uploaded_by"`
	CreatedAt  string `json:"created_at"`
}

type updateInfoJSON struct {
	Latest    *artifactJSON   `json:"latest"`
	Available bool            `json:"available"`
	Pending   *updateSlotJSON `json:"pending"`
}

func (a *app) newAgentsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "Inspect enrolled agents",
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List enrolled agents",
			RunE: func(cmd *cobra.Command, _ []string) error {
				raw, err := a.call(cmd, "agents.list", nil, nil, nil)
				if err != nil {
					return err
				}
				return a.emit(cmd, raw, func() error {
					var list agentListJSON
					if err := decode(raw, "agents.list", &list); err != nil {
						return err
					}
					if len(list.Items) == 0 {
						cmd.Println("No agents found.")
						return nil
					}
					rows := make([][]string, 0, len(list.Items))
					for _, ag := range list.Items {
						rows = append(rows, []string{
							ag.ID, ag.Hostname, ag.Status, ag.Version,
							fmtTime(ag.LastHeartbeatAt),
						})
					}
					writeTable(cmd.OutOrStdout(),
						[]string{"ID", "HOSTNAME", "STATUS", "VERSION", "LAST_HEARTBEAT"}, rows)
					return nil
				})
			},
		},
		&cobra.Command{
			Use:   "show <id>",
			Short: "Show one agent",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				raw, err := a.call(cmd, "agents.show", map[string]string{"id": args[0]}, nil, nil)
				if err != nil {
					return err
				}
				return a.emit(cmd, raw, func() error {
					var ag agentJSON
					if err := decode(raw, "agents.show", &ag); err != nil {
						return err
					}
					writeKV(cmd.OutOrStdout(), [][2]string{
						{"ID", ag.ID},
						{"Hostname", ag.Hostname},
						{"Status", ag.Status},
						{"OS", orDash(ag.OS)},
						{"Arch", orDash(ag.Architecture)},
						{"Version", orDash(ag.Version)},
						{"IP", orDash(ag.IPAddress)},
						{"Disabled", fmtBool(ag.Disabled)},
						{"Outdated", fmtBool(ag.Outdated)},
						{"Capabilities", fmtList(ag.Capabilities)},
						{"Enrolled", ag.EnrolledAt.UTC().Format(time.RFC3339)},
						{"Last heartbeat", fmtTime(ag.LastHeartbeatAt)},
					})
					return nil
				})
			},
		},
		a.newAgentsRevokeCommand(),
		a.newAgentsDeleteCommand(),
		a.newAgentsUpdateStatusCommand(),
		a.newAgentsUpdateCommand(),
	)
	return cmd
}

func (a *app) newAgentsRevokeCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke an agent (irreversible)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"Revoke agent %q? It must re-enroll with a new token. Type the agent ID to continue: ", args[0])
				typed, err := readConfirmLine(cmd)
				if err != nil {
					return fmt.Errorf("confirmation required (use --yes to skip the prompt)")
				}
				if typed != args[0] {
					return fmt.Errorf("confirmation mismatch: aborting")
				}
			}
			raw, err := a.call(cmd, "agents.revoke", map[string]string{"id": args[0]}, nil, nil)
			if err != nil {
				return err
			}
			return a.emit(cmd, raw, func() error {
				cmd.Printf("Revoked agent %s\n", args[0])
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the typed confirmation prompt")
	return cmd
}

func (a *app) newAgentsDeleteCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a revoked agent (irreversible)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"Delete revoked agent %q? The row is removed; job history is kept unlinked. Type the agent ID to continue: ", args[0])
				typed, err := readConfirmLine(cmd)
				if err != nil {
					return fmt.Errorf("confirmation required (use --yes to skip the prompt)")
				}
				if typed != args[0] {
					return fmt.Errorf("confirmation mismatch: aborting")
				}
			}
			raw, err := a.call(cmd, "agents.delete", map[string]string{"id": args[0]}, nil, nil)
			if err != nil {
				return err
			}
			return a.emit(cmd, raw, func() error {
				cmd.Printf("Deleted agent %s\n", args[0])
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the typed confirmation prompt")
	return cmd
}

func (a *app) newAgentsUpdateStatusCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update-status <id>",
		Short: "Show update availability and pending-update state",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := a.call(cmd, "agents.describe", map[string]string{"id": args[0]}, nil, nil)
			if err != nil {
				return err
			}
			return a.emit(cmd, raw, func() error {
				var info updateInfoJSON
				if err := decode(raw, "agents.describe", &info); err != nil {
					return err
				}
				rows := [][2]string{{"Available", fmtBool(info.Available)}}
				if info.Latest != nil {
					rows = append(rows,
						[2]string{"Latest", info.Latest.Version},
						[2]string{"Platform", info.Latest.GOOS + "/" + info.Latest.GOARCH},
					)
				}
				if info.Pending != nil {
					rows = append(rows,
						[2]string{"Pending", info.Pending.TargetVersion + " (" + info.Pending.Status + ")"},
					)
					if info.Pending.Error != "" {
						rows = append(rows, [2]string{"Error", info.Pending.Error})
					}
				}
				writeKV(cmd.OutOrStdout(), rows)
				return nil
			})
		},
	}
	return cmd
}

func (a *app) newAgentsUpdateCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an agent to the latest cached binary",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := a.call(cmd, "agents.describe", map[string]string{"id": args[0]}, nil, nil)
			if err != nil {
				return err
			}
			var info updateInfoJSON
			if err := decode(raw, "agents.describe", &info); err != nil {
				return err
			}
			if !info.Available || info.Latest == nil {
				return a.emit(cmd, raw, func() error {
					cmd.Printf("Agent %s is up to date.\n", args[0])
					return nil
				})
			}
			if !yes {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"Update agent %q to %s (%s/%s)? It restarts itself once idle. Type the agent ID to continue: ",
					args[0], info.Latest.Version, info.Latest.GOOS, info.Latest.GOARCH)
				typed, err := readConfirmLine(cmd)
				if err != nil {
					return fmt.Errorf("confirmation required (use --yes to skip the prompt)")
				}
				if typed != args[0] {
					return fmt.Errorf("confirmation mismatch: aborting")
				}
			}
			raw, err = a.call(cmd, "agents.update", map[string]string{"id": args[0]}, nil,
				map[string]string{"artifact_id": info.Latest.ID})
			if err != nil {
				return err
			}
			return a.emit(cmd, raw, func() error {
				var slot updateSlotJSON
				if err := decode(raw, "agents.update", &slot); err != nil {
					return err
				}
				cmd.Printf("Update to %s requested (%s).\n", slot.TargetVersion, slot.Status)
				return nil
			})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the typed confirmation prompt")
	return cmd
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
