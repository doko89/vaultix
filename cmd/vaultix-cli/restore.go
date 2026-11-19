package main

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// Restore DTOs mirror internal/adapter/http/restore/dto.go (displayed fields
// only): plan request/response, confirm request/confirmation, execute
// request, and the restore job status.
type planRequest struct {
	Kind        string   `json:"kind"`
	SnapshotID  string   `json:"snapshot_id,omitempty"`
	SnapshotIDs []string `json:"snapshot_ids,omitempty"`
	Engine      string   `json:"engine,omitempty"`
	Paths       []string `json:"paths,omitempty"`
	Destination string   `json:"destination,omitempty"`
	TargetDB    string   `json:"target_db,omitempty"`
	Order       []string `json:"order,omitempty"`
	RequestedBy string   `json:"requested_by"`
}

type planJSON struct {
	ID                   string   `json:"id"`
	Kind                 string   `json:"kind"`
	AgentID              string   `json:"agent_id"`
	SnapshotIDs          []string `json:"snapshot_ids"`
	Warnings             []string `json:"warnings"`
	Compatibility        []string `json:"compatibility"`
	AlternateDestination bool     `json:"alternate_destination"`
	RequestedBy          string   `json:"requested_by"`
	CreatedAt            int64    `json:"created_at"`
	ExpiresAt            int64    `json:"expires_at"`
	Steps                []struct {
		Sequence    int      `json:"sequence"`
		Kind        string   `json:"kind"`
		SnapshotID  string   `json:"snapshot_id"`
		Source      string   `json:"source"`
		Destination string   `json:"destination"`
		Notes       []string `json:"notes"`
	} `json:"steps"`
}

type confirmRequest struct {
	PlanID string `json:"plan_id"`
	Actor  string `json:"actor"`
}

type confirmationJSON struct {
	Token     string    `json:"token"`
	PlanID    string    `json:"plan_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type executeRequest struct {
	Token string `json:"token"`
	Actor string `json:"actor"`
}

type restoreJobJSON struct {
	ID           string    `json:"id"`
	AgentID      string    `json:"agent_id"`
	SnapshotID   string    `json:"snapshot_id"`
	TargetPath   string    `json:"target_path"`
	Paths        []string  `json:"paths"`
	State        string    `json:"state"`
	Progress     *float64  `json:"progress"`
	ErrorCode    string    `json:"error_code"`
	ErrorMessage string    `json:"error_message"`
	ApprovedBy   string    `json:"approved_by"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (a *app) newRestoreCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Plan, confirm, execute, and track restores",
	}
	cmd.AddCommand(
		a.newRestorePlanCommand(),
		a.newRestoreConfirmCommand(),
		a.newRestoreExecuteCommand(),
		a.newRestoreStatusCommand(),
	)
	return cmd
}

func (a *app) newRestorePlanCommand() *cobra.Command {
	var kind, snapshot, snapshots, engine, paths, destination, targetDB, order, requestedBy string
	cmd := &cobra.Command{
		Use:   "plan --kind KIND --snapshot ID --requested-by WHO",
		Short: "Plan a restore (POST /api/restore/plan)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			kind = strings.TrimSpace(kind)
			switch kind {
			case "file", "database", "volume", "stack":
			default:
				return fmt.Errorf("invalid --kind %q: want file|database|volume|stack", kind)
			}
			snapshot = strings.TrimSpace(snapshot)
			ids := splitCSV(snapshots)
			if kind == "stack" {
				if len(ids) == 0 {
					return fmt.Errorf("stack restore needs --snapshots (comma-separated ids)")
				}
			} else if snapshot == "" {
				return fmt.Errorf("missing required flag --snapshot")
			}
			if strings.TrimSpace(requestedBy) == "" {
				return fmt.Errorf("missing required flag --requested-by")
			}
			body := planRequest{
				Kind: kind, SnapshotID: snapshot, SnapshotIDs: ids,
				Engine: strings.TrimSpace(engine), Paths: splitCSV(paths),
				Destination: strings.TrimSpace(destination),
				TargetDB:    strings.TrimSpace(targetDB),
				Order:       splitCSV(order), RequestedBy: strings.TrimSpace(requestedBy),
			}
			raw, err := a.call(cmd, "restore.plan", nil, nil, body)
			if err != nil {
				return err
			}
			return a.emit(cmd, raw, func() error {
				var p planJSON
				if err := decode(raw, "restore.plan", &p); err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				fmt.Fprintf(out, "Plan %s (kind %s, agent %s, requested by %s)\n",
					p.ID, p.Kind, orDash(p.AgentID), orDash(p.RequestedBy))
				if len(p.Steps) > 0 {
					rows := make([][]string, 0, len(p.Steps))
					for _, s := range p.Steps {
						rows = append(rows, []string{
							fmt.Sprintf("%d", s.Sequence), s.Kind, s.SnapshotID,
							s.Source, s.Destination, strings.Join(s.Notes, ";"),
						})
					}
					writeTable(out,
						[]string{"SEQ", "KIND", "SNAPSHOT", "SOURCE", "DESTINATION", "NOTES"}, rows)
				}
				for _, w := range p.Warnings {
					fmt.Fprintf(out, "Warning: %s\n", w)
				}
				for _, c := range p.Compatibility {
					fmt.Fprintf(out, "Compatibility: %s\n", c)
				}
				fmt.Fprintf(out, "Confirm with: vaultix-cli restore confirm --plan %s --actor WHO\n", p.ID)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "", "restore kind: file|database|volume|stack (required)")
	cmd.Flags().StringVar(&snapshot, "snapshot", "", "snapshot id (required unless --kind stack)")
	cmd.Flags().StringVar(&snapshots, "snapshots", "", "comma-separated snapshot ids (stack restores)")
	cmd.Flags().StringVar(&engine, "engine", "", "database engine hint")
	cmd.Flags().StringVar(&paths, "paths", "", "comma-separated paths to restore (file restores)")
	cmd.Flags().StringVar(&destination, "destination", "", "alternate destination")
	cmd.Flags().StringVar(&targetDB, "target-db", "", "target database name (database restores)")
	cmd.Flags().StringVar(&order, "order", "", "comma-separated restore order (stack restores)")
	cmd.Flags().StringVar(&requestedBy, "requested-by", "", "requesting user (required)")
	return cmd
}

func (a *app) newRestoreConfirmCommand() *cobra.Command {
	var planID, actor string
	var yes bool
	cmd := &cobra.Command{
		Use:   "confirm --plan ID --actor WHO",
		Short: "Confirm a plan, binding it to a single-use token",
		RunE: func(cmd *cobra.Command, _ []string) error {
			planID = strings.TrimSpace(planID)
			actor = strings.TrimSpace(actor)
			if planID == "" {
				return fmt.Errorf("missing required flag --plan")
			}
			if actor == "" {
				return fmt.Errorf("missing required flag --actor")
			}
			if !yes {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"Confirm restore plan %q as %q? Type the plan ID to continue: ", planID, actor)
				typed, err := readConfirmLine(cmd)
				if err != nil {
					return fmt.Errorf("confirmation required (use --yes to skip the prompt)")
				}
				if typed != planID {
					return fmt.Errorf("confirmation mismatch: aborting")
				}
			}
			raw, err := a.call(cmd, "restore.confirm", nil, nil, confirmRequest{PlanID: planID, Actor: actor})
			if err != nil {
				return err
			}
			return a.emit(cmd, raw, func() error {
				var c confirmationJSON
				if err := decode(raw, "restore.confirm", &c); err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				fmt.Fprintf(out, "Confirmed plan %s\n", c.PlanID)
				fmt.Fprintf(out, "Single-use token (expires %s):\n", c.ExpiresAt.UTC().Format(time.RFC3339))
				fmt.Fprintf(out, "  %s\n", c.Token)
				fmt.Fprintf(out, "Execute with: vaultix-cli restore execute --token <token> --actor %s\n", actor)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&planID, "plan", "", "plan id to confirm (required)")
	cmd.Flags().StringVar(&actor, "actor", "", "confirming user (required)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the typed confirmation prompt")
	return cmd
}

func (a *app) newRestoreExecuteCommand() *cobra.Command {
	var token, actor string
	var yes bool
	cmd := &cobra.Command{
		Use:   "execute --token TOKEN --actor WHO",
		Short: "Execute a confirmed restore (destructive)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			token = strings.TrimSpace(token)
			actor = strings.TrimSpace(actor)
			if token == "" {
				return fmt.Errorf("missing required flag --token")
			}
			if actor == "" {
				return fmt.Errorf("missing required flag --actor")
			}
			if !yes {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"Execute this restore as %q? This writes data. Type EXECUTE to continue: ", actor)
				typed, err := readConfirmLine(cmd)
				if err != nil {
					return fmt.Errorf("confirmation required (use --yes to skip the prompt)")
				}
				if typed != "EXECUTE" {
					return fmt.Errorf("confirmation mismatch: aborting")
				}
			}
			raw, err := a.call(cmd, "restore.execute", nil, nil, executeRequest{Token: token, Actor: actor})
			if err != nil {
				return err
			}
			return a.emit(cmd, raw, func() error {
				var j restoreJobJSON
				if err := decode(raw, "restore.execute", &j); err != nil {
					return err
				}
				cmd.Printf("Restore job %s started (state %s)\n", j.ID, j.State)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&token, "token", "", "single-use confirmation token (required)")
	cmd.Flags().StringVar(&actor, "actor", "", "executing user (required)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the typed confirmation prompt")
	return cmd
}

func (a *app) newRestoreStatusCommand() *cobra.Command {
	var jobID string
	cmd := &cobra.Command{
		Use:   "status --job ID",
		Short: "Show a restore job's status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			jobID = strings.TrimSpace(jobID)
			if jobID == "" {
				return fmt.Errorf("missing required flag --job")
			}
			q := url.Values{}
			q.Set("id", jobID)
			raw, err := a.call(cmd, "restore.status", nil, q, nil)
			if err != nil {
				return err
			}
			return a.emit(cmd, raw, func() error {
				var j restoreJobJSON
				if err := decode(raw, "restore.status", &j); err != nil {
					return err
				}
				writeKV(cmd.OutOrStdout(), [][2]string{
					{"ID", j.ID},
					{"Agent", orDash(j.AgentID)},
					{"Snapshot", orDash(j.SnapshotID)},
					{"Target", orDash(j.TargetPath)},
					{"Paths", fmtList(j.Paths)},
					{"State", j.State},
					{"Progress", fmtProgress(j.Progress)},
					{"Error", orDash(joinNonEmpty(j.ErrorCode, j.ErrorMessage))},
					{"Approved by", orDash(j.ApprovedBy)},
					{"Created", j.CreatedAt.UTC().Format(time.RFC3339)},
					{"Updated", j.UpdatedAt.UTC().Format(time.RFC3339)},
				})
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&jobID, "job", "", "restore job id (required)")
	return cmd
}

// splitCSV splits a comma-separated flag value, dropping blanks.
func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
