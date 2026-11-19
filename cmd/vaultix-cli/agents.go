package main

import (
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
	)
	return cmd
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
