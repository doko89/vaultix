package platform

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-playground/validator/v10"

	"github.com/vaultix/vaultix/internal/domain"
	audituc "github.com/vaultix/vaultix/internal/usecase/audit"
	notificationuc "github.com/vaultix/vaultix/internal/usecase/notification"
)

// Router is the chi-compatible subset this package needs. *chi.Mux satisfies
// it, as does the http root *Mux, so Mount accepts either without importing
// chi. Patterns use chi ":id" syntax; handlers parse IDs from r.URL.Path.
type Router interface {
	Get(pattern string, h http.HandlerFunc)
	Post(pattern string, h http.HandlerFunc)
	Put(pattern string, h http.HandlerFunc)
	Patch(pattern string, h http.HandlerFunc)
	Delete(pattern string, h http.HandlerFunc)
}

// DashboardSource aggregates dashboard data across aggregates.
//
// INTEGRATION NOTE (integrator-2, jobs+agents+policies+storage tracks): no
// single service owns this; integrator-2 writes one dashboard adapter over
// the repos track's repositories (agents, jobs, policies, snapshots) — or
// over the sibling usecases — and passes it as Deps.Dashboard.
type DashboardSource interface {
	AgentCounts(ctx context.Context) (AgentCounts, error)
	JobsByStatus(ctx context.Context) (map[domain.JobState]int64, error)
	RecentJobs(ctx context.Context, limit int) ([]JobSummary, error)
	LastBackupPerAgent(ctx context.Context) ([]LastBackup, error)
	LastBackupPerPolicy(ctx context.Context) ([]LastBackup, error)
	PoliciesWithoutRecentSuccess(ctx context.Context, olderThan time.Duration) ([]PolicyGap, error)
	JobTrend(ctx context.Context, days int) ([]DayPoint, error)
	StorageUsage(ctx context.Context) (StorageUsage, error)
}

// NotificationAdmin is the notification rule surface. *notificationuc.Service
// (this track) implements it.
type NotificationAdmin interface {
	CreateRule(ctx context.Context, r notificationuc.Rule) (notificationuc.Rule, error)
	GetRule(ctx context.Context, id string) (notificationuc.Rule, error)
	ListRules(ctx context.Context) ([]notificationuc.Rule, error)
	UpdateRule(ctx context.Context, id string, r notificationuc.Rule) (notificationuc.Rule, error)
	DeleteRule(ctx context.Context, id string) error
	TestRule(ctx context.Context, id string) error
}

// AuditReader is the audit query surface. *audituc.Service (this track)
// implements it.
type AuditReader interface {
	Query(ctx context.Context, opts domain.ListOptions, f audituc.Filter) (domain.ListResult[domain.AuditLog], error)
}

// SettingsStore persists global manager settings.
//
// INTEGRATION NOTE (repos track): settings need a home. Either the repos
// track adds a settings table (new migration — repos owns schema) with a
// DB-backed store, or settings stay config-file-driven (read-only API).
// Until then serve.go wires MemorySettingsStore.
type SettingsStore interface {
	Get(ctx context.Context) (GlobalSettings, error)
	Update(ctx context.Context, s GlobalSettings) (GlobalSettings, error)
}

// OIDCProviderAdmin administers OIDC providers (CRUD + connection test).
// Read models never carry secrets (see OIDCProviderDTO).
//
// INTEGRATION NOTE (auth track): the auth track implements this over
// domain.OIDCProviderRepository (repos track) plus OIDC discovery/test via
// the oidc adapter. Client secrets are stored encrypted at rest by the auth
// track; this package only transports the write-only secret field.
type OIDCProviderAdmin interface {
	CreateProvider(ctx context.Context, in OIDCProviderInput) (domain.OIDCProvider, error)
	GetProvider(ctx context.Context, id string) (domain.OIDCProvider, error)
	ListProviders(ctx context.Context) ([]domain.OIDCProvider, error)
	UpdateProvider(ctx context.Context, id string, in OIDCProviderInput) (domain.OIDCProvider, error)
	DeleteProvider(ctx context.Context, id string) error
	TestProvider(ctx context.Context, id string) error
}

// Compile-time proof that this track's usecases satisfy the seams.
var (
	_ NotificationAdmin = (*notificationuc.Service)(nil)
	_ AuditReader       = (*audituc.Service)(nil)
)

// Deps wires the package. Sections with nil deps are skipped by Mount (see
// package doc). Validate and Logger default when nil.
type Deps struct {
	Dashboard     DashboardSource
	Notifications NotificationAdmin
	Audit         AuditReader
	Settings      SettingsStore
	OIDC          OIDCProviderAdmin

	Validate *validator.Validate
	Logger   *slog.Logger
}

type handler struct {
	dashboard     DashboardSource
	notifications NotificationAdmin
	audit         AuditReader
	settings      SettingsStore
	oidc          OIDCProviderAdmin
	validate      *validator.Validate
	log           *slog.Logger
}

// Mount registers the platform routes on r, skipping sections with nil deps.
func Mount(r Router, deps Deps) {
	h := &handler{
		dashboard:     deps.Dashboard,
		notifications: deps.Notifications,
		audit:         deps.Audit,
		settings:      deps.Settings,
		oidc:          deps.OIDC,
		validate:      deps.Validate,
		log:           deps.Logger,
	}
	if h.validate == nil {
		h.validate = validator.New()
	}
	if h.log == nil {
		h.log = slog.Default()
	}

	if h.dashboard != nil {
		r.Get("/api/dashboard", h.getDashboard)
	}
	if h.notifications != nil {
		r.Get("/api/notifications/rules", h.listRules)
		r.Post("/api/notifications/rules", h.createRule)
		r.Get("/api/notifications/rules/:id", h.getRule)
		r.Put("/api/notifications/rules/:id", h.updateRule)
		r.Delete("/api/notifications/rules/:id", h.deleteRule)
		r.Post("/api/notifications/rules/:id/test", h.testRule)
	}
	if h.audit != nil {
		r.Get("/api/audit-logs", h.listAuditLogs)
	}
	if h.settings != nil {
		r.Get("/api/settings", h.getSettings)
		r.Put("/api/settings", h.updateSettings)
	}
	if h.oidc != nil {
		r.Get("/api/settings/oidc-providers", h.listProviders)
		r.Post("/api/settings/oidc-providers", h.createProvider)
		r.Get("/api/settings/oidc-providers/:id", h.getProvider)
		r.Put("/api/settings/oidc-providers/:id", h.updateProvider)
		r.Delete("/api/settings/oidc-providers/:id", h.deleteProvider)
		r.Post("/api/settings/oidc-providers/:id/test", h.testProvider)
	}
}
