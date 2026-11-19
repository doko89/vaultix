package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// jobJSON mirrors the jobs JobDTO shape (displayed fields only).
type jobJSON struct {
	ID            string     `json:"id"`
	AgentID       string     `json:"agent_id"`
	PolicyID      string     `json:"policy_id"`
	PolicyVersion int        `json:"policy_version"`
	ScheduleID    string     `json:"schedule_id"`
	Trigger       string     `json:"trigger"`
	State         string     `json:"state"`
	Progress      *float64   `json:"progress"`
	BytesRead     *uint64    `json:"bytes_read"`
	BytesUploaded *uint64    `json:"bytes_uploaded"`
	Attempt       int        `json:"attempt"`
	RetryOf       string     `json:"retry_of"`
	ErrorCode     string     `json:"error_code"`
	ErrorMessage  string     `json:"error_message"`
	SnapshotID    string     `json:"snapshot_id"`
	CreatedBy     string     `json:"created_by"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	StartedAt     *time.Time `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
}

type jobListJSON struct {
	Items  []jobJSON `json:"items"`
	Total  int       `json:"total"`
	Limit  int       `json:"limit"`
	Offset int       `json:"offset"`
}

type jobDetailJSON struct {
	jobJSON
	RetryHistory []struct {
		ID      string `json:"id"`
		Attempt int    `json:"attempt"`
		State   string `json:"state"`
	} `json:"retry_history"`
}

func (a *app) newJobsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "jobs",
		Short: "Inspect and manage backup jobs",
	}

	var listAgent, listPolicy, listState string
	var listLimit, listOffset int
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List backup jobs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			q := url.Values{}
			if listAgent != "" {
				q.Set("agent_id", listAgent)
			}
			if listPolicy != "" {
				q.Set("policy_id", listPolicy)
			}
			if listState != "" {
				q.Set("state", listState)
			}
			if listLimit > 0 {
				q.Set("limit", strconv.Itoa(listLimit))
			}
			if listOffset > 0 {
				q.Set("offset", strconv.Itoa(listOffset))
			}
			raw, err := a.call(cmd, "jobs.list", nil, q, nil)
			if err != nil {
				return err
			}
			return a.emit(cmd, raw, func() error {
				var list jobListJSON
				if err := decode(raw, "jobs.list", &list); err != nil {
					return err
				}
				if len(list.Items) == 0 {
					cmd.Println("No jobs found.")
					return nil
				}
				rows := make([][]string, 0, len(list.Items))
				for _, j := range list.Items {
					rows = append(rows, []string{
						j.ID, j.AgentID, j.PolicyID, j.State,
						fmtProgress(j.Progress),
						strconv.Itoa(j.Attempt),
						j.CreatedAt.UTC().Format(time.RFC3339),
					})
				}
				writeTable(cmd.OutOrStdout(),
					[]string{"ID", "AGENT", "POLICY", "STATE", "PROGRESS", "ATTEMPT", "CREATED"}, rows)
				return nil
			})
		},
	}
	listCmd.Flags().StringVar(&listAgent, "agent", "", "filter by agent id")
	listCmd.Flags().StringVar(&listPolicy, "policy", "", "filter by policy id")
	listCmd.Flags().StringVar(&listState, "state", "", "filter by state (queued|running|succeeded|failed|...)")
	listCmd.Flags().IntVar(&listLimit, "limit", 0, "page size (server default when 0)")
	listCmd.Flags().IntVar(&listOffset, "offset", 0, "page offset")

	showCmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one job with its retry history",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := a.call(cmd, "jobs.show", map[string]string{"id": args[0]}, nil, nil)
			if err != nil {
				return err
			}
			return a.emit(cmd, raw, func() error {
				var d jobDetailJSON
				if err := decode(raw, "jobs.show", &d); err != nil {
					return err
				}
				j := d.jobJSON
				hist := "-"
				if len(d.RetryHistory) > 0 {
					parts := make([]string, 0, len(d.RetryHistory))
					for _, h := range d.RetryHistory {
						parts = append(parts, fmt.Sprintf("%s(#%d,%s)", h.ID, h.Attempt, h.State))
					}
					hist = strings.Join(parts, ",")
				}
				writeKV(cmd.OutOrStdout(), [][2]string{
					{"ID", j.ID},
					{"Agent", j.AgentID},
					{"Policy", fmt.Sprintf("%s (v%d)", j.PolicyID, j.PolicyVersion)},
					{"Trigger", orDash(j.Trigger)},
					{"State", j.State},
					{"Progress", fmtProgress(j.Progress)},
					{"Attempt", strconv.Itoa(j.Attempt)},
					{"Retry of", orDash(j.RetryOf)},
					{"Retry history", hist},
					{"Bytes read", fmtBytes(j.BytesRead)},
					{"Bytes uploaded", fmtBytes(j.BytesUploaded)},
					{"Snapshot", orDash(j.SnapshotID)},
					{"Error", orDash(joinNonEmpty(j.ErrorCode, j.ErrorMessage))},
					{"Created by", orDash(j.CreatedBy)},
					{"Created", j.CreatedAt.UTC().Format(time.RFC3339)},
					{"Started", fmtTime(j.StartedAt)},
					{"Finished", fmtTime(j.FinishedAt)},
				})
				return nil
			})
		},
	}

	retryCmd := &cobra.Command{
		Use:   "retry <id>",
		Short: "Retry a job (server enqueues a new attempt)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := a.call(cmd, "jobs.retry", map[string]string{"id": args[0]}, nil, nil)
			if err != nil {
				return err
			}
			return a.emit(cmd, raw, func() error {
				var j jobJSON
				if err := decode(raw, "jobs.retry", &j); err != nil {
					return err
				}
				cmd.Printf("Retried job %s -> new job %s (state %s)\n", args[0], j.ID, j.State)
				return nil
			})
		},
	}

	cancelCmd := &cobra.Command{
		Use:   "cancel <id>",
		Short: "Cancel a queued or running job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := a.call(cmd, "jobs.cancel", map[string]string{"id": args[0]}, nil, nil)
			if err != nil {
				return err
			}
			return a.emit(cmd, raw, func() error {
				var j jobJSON
				if err := decode(raw, "jobs.cancel", &j); err != nil {
					return err
				}
				cmd.Printf("Cancelled job %s (state %s)\n", j.ID, j.State)
				return nil
			})
		},
	}

	cmd.AddCommand(listCmd, showCmd, retryCmd, cancelCmd)
	return cmd
}

func joinNonEmpty(code, msg string) string {
	switch {
	case code != "" && msg != "":
		return code + ": " + msg
	case code != "":
		return code
	default:
		return msg
	}
}
