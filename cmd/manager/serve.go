package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	adapterhttp "github.com/vaultix/vaultix/internal/adapter/http"
	"github.com/vaultix/vaultix/internal/adapter/http/platform"
)

func newServeCommand(configPath *string) *cobra.Command {
	var skipMigrations bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the Manager (API + scheduler + web UI)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			bindRunFlags(cmd)
			return runServe(cmd.Context(), *configPath, skipMigrations)
		},
	}
	registerFlags(cmd, false)
	cmd.Flags().BoolVar(&skipMigrations, "skip-migrations", false, "skip automatic `migrate up` on boot")
	return cmd
}

func runServe(ctx context.Context, configPath string, skipMigrations bool) error {
	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)
	log.Info("manager starting", "version", version, "config", cfg.Redacted())

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	if !skipMigrations {
		applied, err := Runner{Log: log}.Up(ctx, pgxDB{pool: pool}, cfg.MigrationsDir)
		if err != nil {
			return fmt.Errorf("boot migrations: %w", err)
		}
		log.Info("boot migrations complete", "applied", len(applied))
	}

	// TODO(integrator-2): slice wiring (parallel tracks own these).
	//   - repos track: build the pgx repositories (users, agents, policies,
	//     schedules, jobs, snapshots, storage, notifications, audit, OIDC)
	//     over pool and pass them into the usecases below.
	//   - scheduler: construct scheduler.New with the jobs track's
	//     *job.Service (BackupExecutor — exact method match) and a retention
	//     adapter over the recovery track's *retention.Service
	//     (RetentionEnforcer — see workers.go NOTE); Start it and pass it as
	//     the /healthz queue check. Until then the queue check reports
	//     not_configured (neutral to overall health).
	//   - notifications: build notification senders (webhook endpoints +
	//     SMTP from cfg) and notification.New over the repos outbox; mount
	//     platform Deps.Notifications.
	//   - audit: build audit.New over the repos audit port; mount
	//     platform Deps.Audit.
	//   - dashboard: build the dashboard adapter over repos/usecases
	//     (platform.DashboardSource NOTE); mount platform Deps.Dashboard.
	//   - OIDC admin: pass the auth track's provider admin as
	//     platform Deps.OIDC.
	//   - slices: uncomment the seven Mount calls in the root router and
	//     construct each slice's Deps here (auth, agents, policies, jobs,
	//     restore, storage, platform).
	log.Warn("scheduler workers not wired; queue check reports not_configured (TODO(integrator-2))")

	mux := adapterhttp.NewRouter(adapterhttp.RouterDeps{
		Health: adapterhttp.HealthDeps{
			DB:           pool,
			Queue:        nil, // TODO(integrator-2): scheduler (see above)
			S3Configured: cfg.S3.Configured(),
			Version:      version,
			Logger:       log,
		},
		SPA:            adapterhttp.SPAConfig{Dir: cfg.StaticDir},
		RequestTimeout: cfg.RequestTimeout(),
		Logger:         log,
	})
	// Only the settings section is satisfiable in this worktree (in-memory
	// store); dashboard/notifications/audit/OIDC mount with integrator-2
	// wiring (see TODO block above).
	platform.Mount(mux, platform.Deps{
		Settings: platform.NewMemorySettingsStore(platform.DefaultGlobalSettings()),
		Logger:   log,
	})

	srv := adapterhttp.Server{
		Addr:            cfg.Addr,
		Handler:         mux,
		ShutdownTimeout: cfg.ShutdownTimeout(),
		Logger:          log,
	}
	log.Info("manager serving", "addr", cfg.Addr)
	return srv.Run(ctx)
}

func newLogger(level string) *slog.Logger {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lv}))
}
