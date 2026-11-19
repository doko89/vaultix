package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/vaultix/vaultix/internal/agent"
)

// defaultConfigPath is used when --config is not given.
func defaultConfigPath() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "vaultix", "agent.yaml")
	}
	return filepath.Join(".", "agent.yaml")
}

func newRootCommand() *cobra.Command {
	var configPath string
	root := &cobra.Command{
		Use:           "agent",
		Short:         "Vaultix Agent: per-VM backup executor",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&configPath, "config", "", "config file path (default "+defaultConfigPath()+")")
	root.AddCommand(newEnrollCommand(&configPath), newRunCommand(&configPath), newVersionCommand())
	return root
}

func resolveConfigPath(flag string) string {
	if strings.TrimSpace(flag) != "" {
		return flag
	}
	if env := strings.TrimSpace(os.Getenv("VAULTIX_AGENT_CONFIG")); env != "" {
		return env
	}
	return defaultConfigPath()
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print agent version",
		Run: func(cmd *cobra.Command, _ []string) {
			cmd.Println("agent", version)
		},
	}
}

func newEnrollCommand(configPath *string) *cobra.Command {
	var managerURL, token, agentID string
	cmd := &cobra.Command{
		Use:   "enroll --manager <url> --token <token>",
		Short: "Enroll this VM with the Manager and persist credentials",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(managerURL) == "" {
				return fmt.Errorf("missing required flag --manager")
			}
			if strings.TrimSpace(token) == "" {
				return fmt.Errorf("missing required flag --token")
			}
			path := resolveConfigPath(*configPath)
			cfg, err := agent.Load(path)
			if err != nil {
				return err
			}
			if strings.TrimSpace(agentID) == "" {
				agentID, err = generateAgentID()
				if err != nil {
					return err
				}
			}
			cfg.ManagerURL = managerURL
			cfg.Token = token
			cfg.AgentID = agentID
			if err := cfg.Validate(); err != nil {
				return err
			}
			if err := cfg.Save(path); err != nil {
				return err
			}
			// Never print the token: enrollment output carries only the
			// manager URL, agent ID, and config path.
			cmd.Printf("enrolled agent %s with manager %s (config %s)\n", agentID, managerURL, path)
			return nil
		},
	}
	cmd.Flags().StringVar(&managerURL, "manager", "", "Manager base URL, e.g. https://manager.example:8443")
	cmd.Flags().StringVar(&token, "token", "", "enrollment token issued by the Manager")
	cmd.Flags().StringVar(&agentID, "agent-id", "", "agent identity (generated when omitted)")
	return cmd
}

func generateAgentID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate agent id: %w", err)
	}
	return "agent-" + hex.EncodeToString(raw[:]), nil
}

func newRunCommand(configPath *string) *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run the agent daemon (heartbeat, discovery, job polling)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := resolveConfigPath(*configPath)
			cfg, err := agent.Load(path)
			if err != nil {
				return err
			}
			if err := cfg.Validate(); err != nil {
				return err
			}
			log := slog.New(slog.NewJSONHandler(cmd.ErrOrStderr(), &slog.HandlerOptions{
				Level: slog.LevelInfo,
			}))
			// Log the redacted config: manager URL and agent ID are safe,
			// the credential is masked.
			log.Info("agent starting", "version", version, "config", cfg.Redacted())

			store, err := agent.NewStore(cfg.StateFile)
			if err != nil {
				return err
			}
			if pending := store.Incomplete(); len(pending) > 0 {
				log.Info("found incomplete jobs from previous run", "count", len(pending))
				for _, rec := range pending {
					log.Info("incomplete job", "job_id", rec.JobID, "state", rec.State)
				}
			}

			if err := runDiscovery(cmd.Context(), cfg, log); err != nil {
				log.Warn("initial discovery failed", "error", err)
			}

			mgr := agent.NewHTTPManagerClient(cfg.ManagerURL, cfg.Token, cfg.RequestTimeout)
			runner := &agent.HeartbeatRunner{Client: mgr, Config: cfg, Version: version, Logger: log}
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			log.Info("heartbeat loop started", "interval", cfg.HeartbeatInterval.String())
			if err := runner.Run(ctx); err != nil {
				return err
			}
			log.Info("agent stopped")
			return nil
		},
	}
}

func runDiscovery(ctx context.Context, cfg agent.Config, log *slog.Logger) error {
	var lister agent.DockerLister
	if cfg.DockerEnabled {
		cli, err := agent.NewMobyClient(cfg.DockerHost)
		if err != nil {
			return err
		}
		defer func() { _ = cli.Close() }()
		lister = cli
	}
	discoverer := agent.NewDiscoverer(cfg.DockerEnabled, lister, cfg.RequestTimeout)
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	report, err := discoverer.Discover(dctx)
	if err != nil {
		return err
	}
	if report.Disabled {
		log.Info("docker discovery disabled")
		return nil
	}
	log.Info("docker discovery complete",
		"containers", len(report.Containers), "volumes", len(report.Volumes))
	return nil
}
