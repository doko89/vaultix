package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/vaultix/vaultix/internal/adapter/crypto"
	adapterhttp "github.com/vaultix/vaultix/internal/adapter/http"
	agentshttp "github.com/vaultix/vaultix/internal/adapter/http/agents"
	apikeyshttp "github.com/vaultix/vaultix/internal/adapter/http/apikeys"
	authhttp "github.com/vaultix/vaultix/internal/adapter/http/auth"
	jobshttp "github.com/vaultix/vaultix/internal/adapter/http/jobs"
	platformhttp "github.com/vaultix/vaultix/internal/adapter/http/platform"
	policieshttp "github.com/vaultix/vaultix/internal/adapter/http/policies"
	restorehttp "github.com/vaultix/vaultix/internal/adapter/http/restore"
	storagehttp "github.com/vaultix/vaultix/internal/adapter/http/storage"
	"github.com/vaultix/vaultix/internal/adapter/oidc"
	"github.com/vaultix/vaultix/internal/adapter/repo"
	"github.com/vaultix/vaultix/internal/adapter/s3"
	"github.com/vaultix/vaultix/internal/adapter/scheduler"
	"github.com/vaultix/vaultix/internal/adapter/session"
	"github.com/vaultix/vaultix/internal/domain"
	agentuc "github.com/vaultix/vaultix/internal/usecase/agent"
	apikeyuc "github.com/vaultix/vaultix/internal/usecase/apikey"
	audituc "github.com/vaultix/vaultix/internal/usecase/audit"
	authuc "github.com/vaultix/vaultix/internal/usecase/auth"
	backupuc "github.com/vaultix/vaultix/internal/usecase/backup"
	discoveryuc "github.com/vaultix/vaultix/internal/usecase/discovery"
	jobuc "github.com/vaultix/vaultix/internal/usecase/job"
	notificationuc "github.com/vaultix/vaultix/internal/usecase/notification"
	policyuc "github.com/vaultix/vaultix/internal/usecase/policy"
	restoreuc "github.com/vaultix/vaultix/internal/usecase/restore"
	retentionuc "github.com/vaultix/vaultix/internal/usecase/retention"
	scheduleuc "github.com/vaultix/vaultix/internal/usecase/schedule"
	storageuc "github.com/vaultix/vaultix/internal/usecase/storage"
	useruc "github.com/vaultix/vaultix/internal/usecase/user"
)

func newServeCommand(configPath *string) *cobra.Command {
	var skipMigrations bool
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the Manager (API + scheduler + web UI)",
		Long: `Run the Manager (API + scheduler + web UI).

TLS: pass --tls-cert and --tls-key (or VAULTIX_TLS_CERT / VAULTIX_TLS_KEY)
to serve HTTPS (TLS 1.2+); with neither set the manager serves plain HTTP
with a loud warning. Encrypted-backup data keys are issued only over a
secure transport: direct TLS, or X-Forwarded-Proto: https from a proxy
listed in VAULTIX_TRUSTED_PROXIES (comma-separated CIDRs, empty = none).

Session cookies follow the same transport gate by default: --cookie-secure
(or VAULTIX_COOKIE_SECURE, or cookie_secure in the config file) takes
auto|on|off (default auto). auto sets the Secure attribute only when the
request arrived over a secure transport; on always sets it (strict TLS
production); off never sets it (explicit local plain-HTTP development).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			bindRunFlags(cmd)
			bindTLSFlags(cmd)
			bindCookieSecureFlag(cmd)
			return runServe(cmd.Context(), *configPath, skipMigrations)
		},
	}
	registerFlags(cmd, false)
	cmd.Flags().BoolVar(&skipMigrations, "skip-migrations", false, "skip automatic `migrate up` on boot")
	cmd.Flags().String("tls-cert", "", "TLS certificate PEM file for HTTPS (env VAULTIX_TLS_CERT); requires --tls-key")
	cmd.Flags().String("tls-key", "", "TLS private key PEM file for HTTPS (env VAULTIX_TLS_KEY); requires --tls-cert")
	cmd.Flags().String("cookie-secure", "auto", "session cookie Secure attribute: auto|on|off (env VAULTIX_COOKIE_SECURE)")
	return cmd
}

// cookieSecureKey is the viper key for the session-cookie Secure mode.
// Precedence: flag > env (VAULTIX_COOKIE_SECURE) > config file > default.
const cookieSecureKey = "cookie_secure"

// bindCookieSecureFlag binds the serve-local --cookie-secure flag to viper.
// It runs inside RunE next to bindRunFlags/bindTLSFlags so the EXECUTING
// command's parsed FlagSet wins; un-changed flags defer to env/config.
func bindCookieSecureFlag(cmd *cobra.Command) {
	if f := cmd.Flags().Lookup("cookie-secure"); f != nil {
		_ = viper.BindPFlag(cookieSecureKey, f)
	}
	_ = viper.BindEnv(cookieSecureKey)
}

// resolveCookieSecure returns the configured session-cookie Secure mode,
// failing fast on anything but auto|on|off.
func resolveCookieSecure() (authhttp.SecureMode, error) {
	return authhttp.ParseSecureMode(viper.GetString(cookieSecureKey))
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
	certFile, keyFile := resolveTLSFiles()
	if err := validateTLSFiles(certFile, keyFile); err != nil {
		return err
	}
	if err := agentshttp.SetTrustedProxies(resolveTrustedProxies()); err != nil {
		return fmt.Errorf("trusted proxies: %w", err)
	}
	cookieMode, err := resolveCookieSecure()
	if err != nil {
		return err
	}
	logTransportMode(log, certFile)
	log.Debug("session cookie Secure mode", "mode", string(cookieMode))
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

	w, err := wire(ctx, pool, cfg, log, cookieMode)
	if err != nil {
		return err
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout())
		defer cancel()
		if err := w.sched.Stop(stopCtx); err != nil {
			log.Warn("scheduler stop failed", "error", err)
		}
	}()

	mux := adapterhttp.NewRouter(adapterhttp.RouterDeps{
		Health: adapterhttp.HealthDeps{
			DB:           pool,
			Queue:        w.sched,
			S3Configured: cfg.S3.Configured(),
			Version:      version,
			Logger:       log,
		},
		SPA:            adapterhttp.SPAConfig{Dir: cfg.StaticDir},
		RequestTimeout: cfg.RequestTimeout(),
		Logger:         log,
		CSRFCookie:     sessionCookieName,
		Slices:         w.slices,
	})

	srv := adapterhttp.Server{
		Addr:            cfg.Addr,
		Handler:         mux,
		ShutdownTimeout: cfg.ShutdownTimeout(),
		CertFile:        certFile,
		KeyFile:         keyFile,
		Logger:          log,
	}
	log.Info("manager serving", "addr", cfg.Addr)
	return srv.Run(ctx)
}

// sessionCookieName is the session cookie guarded by the CSRF middleware;
// it matches the auth slice default (Cookie.Name unset below).
const sessionCookieName = "vaultix_session"

// wired carries the scheduler (for health + shutdown) and the assembled
// slice deps for the root router.
type wired struct {
	sched  *scheduler.Scheduler
	slices adapterhttp.SliceDeps
}

// wire builds every repository, usecase, and adapter, starts the
// scheduler and the background tickers, and returns the router deps.
func wire(ctx context.Context, pool *pgxpool.Pool, cfg Config, log *slog.Logger, cookieMode authhttp.SecureMode) (*wired, error) {
	// Crypto box backs storage SecretBox AND auth provider secrets.
	box, err := crypto.NewBoxFromEnv("")
	if err != nil {
		return nil, fmt.Errorf("crypto key (%s): %w", crypto.DefaultKeyEnvVar, err)
	}

	repos := repo.New(pool)
	sessions := session.New(pool)

	// Storage: SecretBox adapter + S3 registry resolving destination rows
	// through their encrypted credential profiles.
	storageBox := &storageSecretBox{box: box}
	registry := s3.NewRegistry(s3ResolveFunc(repos, storageBox), nil)
	storageSvc := storageuc.NewService(repos.Storage, registry, storageBox)

	auditSvc, err := audituc.New(repos.Audit, nil)
	if err != nil {
		return nil, fmt.Errorf("audit service: %w", err)
	}
	sink := &auditSink{audit: auditSvc}

	// Auth: the OIDC client resolves secrets through the auth service,
	// which needs the client — the closure breaks the cycle.
	var authSvc *authuc.Service
	oidcClient := oidc.New(oidc.WithSecretResolver(
		func(ctx context.Context, p *domain.OIDCProvider) (string, error) {
			return authSvc.ProviderSecret(ctx, p.ID)
		}))
	settings := platformhttp.DefaultGlobalSettings()
	authSvc, err = authuc.NewService(authuc.Deps{
		Users:     repos.Users,
		Providers: repos.OIDC,
		Sessions:  sessions,
		OIDC:      oidcClient,
		Tester:    oidcClient,
		Secrets:   providerSecretStore{pool: pool},
		Box:       box,
		Hash:      authuc.BcryptHasher{},
		Audit:     sink,
		Config: authuc.Config{
			SessionAbsoluteTTL: time.Duration(settings.SessionLifetimeMinutes) * time.Minute,
			SessionIdleTimeout: time.Duration(settings.SessionIdleMinutes) * time.Minute,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("auth service: %w", err)
	}
	userSvc, err := useruc.NewService(useruc.Deps{
		Repo:     repos.Users,
		Hash:     authuc.BcryptHasher{},
		Sessions: sessions,
		Audit:    sink,
	})
	if err != nil {
		return nil, fmt.Errorf("user service: %w", err)
	}
	apikeySvc, err := apikeyuc.NewService(apikeyuc.Deps{
		Keys: repo.NewApiKeyRepo(pool), Users: repos.Users, Audit: sink,
	})
	if err != nil {
		return nil, fmt.Errorf("apikey service: %w", err)
	}

	heartbeatTimeout := time.Duration(cfg.HeartbeatTimeoutSecs) * time.Second
	agentSvc, err := agentuc.NewService(repos.Agents, repos.Policies, domain.SystemClock{},
		agentuc.WithHeartbeatTimeout(heartbeatTimeout))
	if err != nil {
		return nil, fmt.Errorf("agent service: %w", err)
	}
	discoverySvc, err := discoveryuc.NewService(repos.Discovery, repos.Agents, domain.SystemClock{})
	if err != nil {
		return nil, fmt.Errorf("discovery service: %w", err)
	}

	policySvc := policyuc.NewService(repos.Policies,
		policyuc.WithStorage(repos.Storage), policyuc.WithStacks(repos.Stacks))
	globalLimit := settings.GlobalConcurrency
	scheduleSvc := scheduleuc.NewService(repos.Schedules, repos.Policies,
		scheduleuc.WithLimits(0, globalLimit))

	// Cancellations are tracked in memory (single-instance scope) so agent
	// polls surface cancelled_job_ids; the persisted cancelled state stays
	// authoritative and ReportResult revalidates regardless.
	jobSvc := jobuc.NewService(jobuc.Deps{
		Jobs:     repos.BackupJobs,
		Agents:   repos.Agents,
		Policies: repos.Policies,
		Cancels:  jobuc.NewMemoryCancelNotifier(),
		Limits:   jobuc.ConcurrencyLimits{MaxGlobal: globalLimit},
	})

	backupSvc, err := backupuc.New(backupuc.Deps{
		Policies: repos.Policies, Jobs: repos.BackupJobs,
		Snapshots: repos.Snapshots, Stacks: repos.Stacks,
		Objects: objectHead{objects: registry},
	})
	if err != nil {
		return nil, fmt.Errorf("backup service: %w", err)
	}

	retentionSvc, err := retentionuc.New(retentionuc.Deps{
		Snapshots: repos.Snapshots,
		Objects:   objectDelete{objects: registry},
	})
	if err != nil {
		return nil, fmt.Errorf("retention service: %w", err)
	}
	restoreSvc, err := restoreuc.New(restoreuc.Deps{
		Snapshots: repos.Snapshots,
		Reporter:  restoreReporter{repo: repos.RestoreJobs},
	})
	if err != nil {
		return nil, fmt.Errorf("restore service: %w", err)
	}

	enqueuer := &scheduleJobEnqueuer{jobs: jobSvc, policies: repos.Policies, repo: repos.BackupJobs}
	gate := &jobConcurrencyGate{repo: repos.BackupJobs}

	sched, err := scheduler.New(
		scheduler.Config{ShutdownTimeout: cfg.ShutdownTimeout(), Logger: log},
		jobSvc, &retentionEnforcer{retention: retentionSvc, rules: repos.Retention, log: log})
	if err != nil {
		return nil, fmt.Errorf("scheduler: %w", err)
	}
	if err := sched.Start(ctx); err != nil {
		return nil, fmt.Errorf("scheduler start: %w", err)
	}
	runner := &recurringRunner{sched: sched}

	notifSvc, err := wireNotifications(cfg, repos, log)
	if err != nil {
		return nil, err
	}

	go runSessionJanitor(ctx, log, sessions, cfg.JanitorInterval())
	go runDispatcher(ctx, log, scheduleSvc, runner, gate, notifSvc, cfg.DispatchInterval())

	keys := keyIssuerFor(repos, storageBox, auditSvc, log)
	dataPlane := &agentDataPlane{
		jobs: jobSvc, restores: repos.RestoreJobs, snapshots: repos.Snapshots,
		storage: storageSvc, store: repos.Storage,
		keys: keys, clock: domain.SystemClock{},
	}

	return &wired{
		sched: sched,
		slices: adapterhttp.SliceDeps{
			Auth: authhttp.Deps{
				Auth:  authSvc,
				Users: userSvc,
				Audit: sink,
				Keys:  apikeySvc,
				// Secure follows the cookie_secure mode (default auto:
				// per-request transport via the agent-track probe);
				// name/path/SameSite take the slice defaults.
				Cookie: authhttp.CookieConfig{
					SecureMode: cookieMode,
					IsSecure:   agentshttp.IsSecure,
				},
				Logger:               log,
				OIDCRedirectURL:      cfg.OIDCRedirectURL,
				UIRedirectAfterLogin: "/",
			},
			APIKeys: apikeyshttp.Deps{Service: apikeySvc},
			Agents: agentshttp.Deps{
				Agents:    agentSvc,
				Discovery: discoverySvc,
				Jobs: &agentJobQueue{
					jobs: jobSvc, agents: repos.Agents, policies: repos.Policies,
					backup: backupSvc, keys: keys, log: log,
				},
				Grants:   dataPlane,
				Restores: dataPlane,
				Logger:   log,
			},
			Policies: policieshttp.Deps{
				Policies:  policySvc,
				Schedules: scheduleSvc,
				Enqueuer:  enqueuer,
				Gate:      gate,
			},
			Jobs: jobshttp.Deps{Service: jobSvc},
			Restore: restorehttp.Deps{
				Snapshots: repos.Snapshots,
				Restores:  restoreSvc,
			},
			Storage: storagehttp.Deps{Service: storageSvc},
			Platform: platformhttp.Deps{
				Dashboard: &dashboardSource{
					agents: repos.Agents, jobs: repos.BackupJobs,
					policies: repos.Policies, snapshots: repos.Snapshots,
					clock: domain.SystemClock{}, heartbeatTimeout: heartbeatTimeout,
				},
				Notifications: notifSvc,
				Audit:         auditSvc,
				Settings:      platformhttp.NewMemorySettingsStore(settings),
				OIDC:          &oidcProviderAdmin{auth: authSvc, pool: pool},
				Logger:        log,
			},
		},
	}, nil
}

// keyIssuerFor builds the data-key issuer over the storage secret profiles,
// the envelope box, and the audit trail.
func keyIssuerFor(repos *repo.Repos, box *storageSecretBox, audit *audituc.Service, log *slog.Logger) *keyIssuer {
	return &keyIssuer{store: repos.Storage, box: box, audit: audit, log: log}
}

// s3ResolveFunc loads a destination row plus its credential profile,
// opening the secret through the storage SecretBox. Storage credential
// profiles hold JSON: {"access_key_id","secret_access_key"}. Resolve
// errors never embed secret material.
func s3ResolveFunc(repos *repo.Repos, box *storageSecretBox) s3.ResolveFunc {
	return func(ctx context.Context, destinationID string) (s3.Config, error) {
		d, err := repos.Storage.GetDestination(ctx, destinationID)
		if err != nil {
			return s3.Config{}, err
		}
		if d.CredentialRef == "" {
			return s3.Config{}, fmt.Errorf("s3: destination %q has no credential profile", d.Name)
		}
		sec, err := repos.Storage.GetSecret(ctx, d.CredentialRef)
		if err != nil {
			return s3.Config{}, fmt.Errorf("s3: destination %q credential unavailable", d.Name)
		}
		plain, err := box.Open(ctx, sec.Ciphertext, sec.KeyRef)
		if err != nil {
			return s3.Config{}, fmt.Errorf("s3: destination %q credential undecryptable", d.Name)
		}
		var cred struct {
			AccessKeyID     string `json:"access_key_id"`
			SecretAccessKey string `json:"secret_access_key"`
		}
		if err := json.Unmarshal(plain, &cred); err != nil {
			return s3.Config{}, fmt.Errorf("s3: destination %q credential is not valid JSON", d.Name)
		}
		if cred.AccessKeyID == "" || cred.SecretAccessKey == "" {
			return s3.Config{}, fmt.Errorf("s3: destination %q credential is incomplete", d.Name)
		}
		return s3.Config{
			Endpoint: d.Endpoint, Region: d.Region, Bucket: d.Bucket, Prefix: d.Prefix,
			AccessKeyID: cred.AccessKeyID, SecretAccessKey: cred.SecretAccessKey,
			UsePathStyle: d.UsePathStyle,
		}, nil
	}
}

// wireNotifications builds the notification service: webhook sender always
// (no endpoints configured — rules resolve names server-side), SMTP sender
// when configured.
func wireNotifications(cfg Config, repos *repo.Repos, log *slog.Logger) (*notificationuc.Service, error) {
	rules, err := notificationuc.NewStaticRuleStore(nil)
	if err != nil {
		return nil, fmt.Errorf("notification rules: %w", err)
	}
	webhook, err := notificationuc.NewWebhookSender(
		notificationuc.WebhookConfig{Endpoints: map[string]notificationuc.WebhookEndpoint{}},
		nil, log)
	if err != nil {
		return nil, fmt.Errorf("webhook sender: %w", err)
	}
	senders := []notificationuc.Sender{webhook}
	if cfg.SMTP.Host != "" {
		smtp, err := notificationuc.NewSMTPSender(smtpConfig(cfg.SMTP), log)
		if err != nil {
			return nil, fmt.Errorf("smtp sender: %w", err)
		}
		senders = append(senders, smtp)
	} else {
		log.Info("smtp not configured; email notifications will fail until configured")
	}
	svc, err := notificationuc.New(notificationuc.Deps{
		Rules: rules, Outbox: repos.Notifications, Senders: senders, Logger: log,
	})
	if err != nil {
		return nil, fmt.Errorf("notification service: %w", err)
	}
	return svc, nil
}

// smtpConfig converts the config-file SMTP shape (seconds) to the
// notification track's durations.
func smtpConfig(c SMTPConfig) notificationuc.SMTPConfig {
	return notificationuc.SMTPConfig{
		Host: c.Host, Port: c.Port, From: c.From, Auth: c.Auth,
		Username: c.Username, Password: c.Password, TLS: c.TLS,
		DialTimeout: time.Duration(c.DialTimeoutSecs) * time.Second,
		IOTimeout:   time.Duration(c.IOTimeoutSecs) * time.Second,
	}
}

// runSessionJanitor periodically deletes expired sessions.
func runSessionJanitor(ctx context.Context, log *slog.Logger, sessions *session.Store, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := sessions.DeleteExpired(ctx, time.Now())
			if err != nil {
				log.Warn("session janitor failed", "error", err)
				continue
			}
			if n > 0 {
				log.Info("session janitor removed expired sessions", "count", n)
			}
		}
	}
}

// runDispatcher ticks schedule dispatch plus notification redelivery.
// DispatchDue only advances NextRunAt for nil-error runs (one-time fires
// retire); failures are logged per schedule without aborting the tick.
func runDispatcher(ctx context.Context, log *slog.Logger, schedules *scheduleuc.Service, runner *recurringRunner, gate *jobConcurrencyGate, notif *notificationuc.Service, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := time.Now()
			rep, err := schedules.DispatchDue(ctx, now, runner, gate)
			if err != nil {
				log.Warn("schedule dispatch failed", "error", err)
			} else if len(rep.Fired)+len(rep.Failed) > 0 {
				log.Info("schedule tick",
					"fired", len(rep.Fired), "skipped", len(rep.Skipped), "failed", len(rep.Failed))
				for _, f := range rep.Failed {
					log.Warn("schedule fire failed", "schedule_id", f.ScheduleID, "error", f.Err)
				}
			}
			if n, err := notif.DrainPending(ctx, 100); err != nil {
				log.Warn("notification drain failed", "error", err)
			} else if n > 0 {
				log.Info("notification drain delivered", "count", n)
			}
		}
	}
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
