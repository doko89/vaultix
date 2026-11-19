package main

import (
	"net/url"
	"strconv"
	"time"

	"github.com/spf13/cobra"
)

// snapshotJSON mirrors the restore SnapshotDTO shape (displayed fields only).
type snapshotJSON struct {
	ID            string     `json:"id"`
	AgentID       string     `json:"agent_id"`
	PolicyID      string     `json:"policy_id"`
	PolicyVersion int        `json:"policy_version"`
	JobID         string     `json:"job_id"`
	TargetType    string     `json:"target_type"`
	Selector      string     `json:"selector"`
	Status        string     `json:"status"`
	BytesOriginal *uint64    `json:"bytes_original"`
	BytesStored   *uint64    `json:"bytes_stored"`
	ObjectCount   int        `json:"object_count"`
	Encryption    string     `json:"encryption"`
	StorageID     string     `json:"storage_id"`
	Restorable    bool       `json:"restorable"`
	CompletedAt   *time.Time `json:"completed_at"`
	CreatedAt     time.Time  `json:"created_at"`
}

type catalogListJSON struct {
	Items  []snapshotJSON `json:"items"`
	Total  int            `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}

func (a *app) newCatalogCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "Browse the snapshot catalog",
	}

	var filterAgent, filterPolicy, filterTarget string
	var restorableOnly bool
	var limit, offset int
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List snapshots",
		RunE: func(cmd *cobra.Command, _ []string) error {
			q := url.Values{}
			if filterAgent != "" {
				q.Set("agent_id", filterAgent)
			}
			if filterPolicy != "" {
				q.Set("policy_id", filterPolicy)
			}
			if filterTarget != "" {
				q.Set("target", filterTarget)
			}
			if restorableOnly {
				q.Set("restorable_only", "true")
			}
			if limit > 0 {
				q.Set("limit", strconv.Itoa(limit))
			}
			if offset > 0 {
				q.Set("offset", strconv.Itoa(offset))
			}
			raw, err := a.call(cmd, "catalog.list", nil, q, nil)
			if err != nil {
				return err
			}
			return a.emit(cmd, raw, func() error {
				var list catalogListJSON
				if err := decode(raw, "catalog.list", &list); err != nil {
					return err
				}
				if len(list.Items) == 0 {
					cmd.Println("No snapshots found.")
					return nil
				}
				rows := make([][]string, 0, len(list.Items))
				for _, s := range list.Items {
					rows = append(rows, []string{
						s.ID, s.AgentID, s.PolicyID, s.TargetType, s.Selector,
						s.Status, fmtBool(s.Restorable), fmtTime(s.CompletedAt),
					})
				}
				writeTable(cmd.OutOrStdout(),
					[]string{"ID", "AGENT", "POLICY", "TARGET", "SELECTOR", "STATUS", "RESTORABLE", "COMPLETED"}, rows)
				return nil
			})
		},
	}
	listCmd.Flags().StringVar(&filterAgent, "agent", "", "filter by agent id")
	listCmd.Flags().StringVar(&filterPolicy, "policy", "", "filter by policy id")
	listCmd.Flags().StringVar(&filterTarget, "target", "", "filter by target type")
	listCmd.Flags().BoolVar(&restorableOnly, "restorable-only", false, "only restorable snapshots")
	listCmd.Flags().IntVar(&limit, "limit", 0, "page size (server default when 0)")
	listCmd.Flags().IntVar(&offset, "offset", 0, "page offset")

	cmd.AddCommand(listCmd)
	return cmd
}
