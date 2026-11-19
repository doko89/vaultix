package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// createJobRequest mirrors the jobs CreateJobRequest body.
type createJobRequest struct {
	AgentID  string `json:"agent_id"`
	PolicyID string `json:"policy_id"`
}

func (a *app) newBackupCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Trigger backups",
	}

	var policyID, agentID string
	runCmd := &cobra.Command{
		Use:   "run --policy ID [--agent ID]",
		Short: "Trigger a manual backup (POST /api/jobs/)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(policyID) == "" {
				return fmt.Errorf("missing required flag --policy")
			}
			agent := strings.TrimSpace(agentID)
			if agent == "" {
				// Fan-out is a server concern; the CLI resolves the common
				// single-agent case and asks otherwise.
				raw, err := a.call(cmd, "policies.show", map[string]string{"id": policyID}, nil, nil)
				if err != nil {
					return err
				}
				var p policyJSON
				if err := decode(raw, "policies.show", &p); err != nil {
					return err
				}
				switch len(p.AgentIDs) {
				case 0:
					return fmt.Errorf("policy %s targets no agents: specify --agent explicitly", policyID)
				case 1:
					agent = p.AgentIDs[0]
				default:
					return fmt.Errorf("policy %s targets %d agents: specify --agent explicitly", policyID, len(p.AgentIDs))
				}
			}
			raw, err := a.call(cmd, "jobs.create", nil, nil, createJobRequest{AgentID: agent, PolicyID: policyID})
			if err != nil {
				return err
			}
			return a.emit(cmd, raw, func() error {
				var j jobJSON
				if err := decode(raw, "jobs.create", &j); err != nil {
					return err
				}
				cmd.Printf("Started backup job %s (agent %s, policy %s, state %s)\n",
					j.ID, j.AgentID, j.PolicyID, j.State)
				return nil
			})
		},
	}
	runCmd.Flags().StringVar(&policyID, "policy", "", "policy id to back up (required)")
	runCmd.Flags().StringVar(&agentID, "agent", "", "agent id (defaults to the policy's agent when it targets exactly one)")

	cmd.AddCommand(runCmd)
	return cmd
}
