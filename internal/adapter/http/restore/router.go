package restore

import (
	"context"
	"net/http"

	"github.com/vaultix/vaultix/internal/domain"
	restoreuc "github.com/vaultix/vaultix/internal/usecase/restore"
)

// Router is the minimal routing surface Mount needs. chi.Router satisfies
// it (identical Get/Post method shapes), so the manager passes its chi
// router at wiring time (see INTEGRATION NOTE in Mount). Query parameters
// carry resource IDs so no framework-specific path-parameter helper is
// needed.
type Router interface {
	Get(pattern string, h http.HandlerFunc)
	Post(pattern string, h http.HandlerFunc)
}

// RestoreService is the restore usecase surface these handlers need.
// *restoreuc.Service implements it.
type RestoreService interface {
	Plan(ctx context.Context, req restoreuc.PlanRequest) (*restoreuc.Plan, error)
	Confirm(ctx context.Context, planID, actor string) (*restoreuc.Confirmation, error)
	Execute(ctx context.Context, token, actor string) (*domain.RestoreJob, error)
	Status(ctx context.Context, jobID string) (*domain.RestoreJob, error)
}

// Deps wires the handlers.
type Deps struct {
	Snapshots domain.SnapshotRepository
	Restores  RestoreService
}

// Mount registers catalog and restore routes on r.
//
// INTEGRATION NOTE: the manager (http track) calls
// Mount(chiRouter, Deps{Snapshots: repo, Restores: restoreService}) once it
// adds github.com/go-chi/chi/v5 — *chi.Mux already satisfies Router, so no
// signature change is needed. Routes use query-string IDs and plain
// GET/POST methods only.
func Mount(r Router, deps Deps) {
	if r == nil {
		panic("http/restore: router is required")
	}
	if deps.Snapshots == nil || deps.Restores == nil {
		panic("http/restore: snapshots repository and restore service are required")
	}
	h := &handler{snapshots: deps.Snapshots, restores: deps.Restores}

	r.Get("/api/catalog", h.listCatalog)
	r.Get("/api/catalog/snapshot", h.getSnapshot)
	r.Get("/api/catalog/manifest", h.getManifest)

	r.Post("/api/restore/plan", h.planRestore)
	r.Post("/api/restore/confirm", h.confirmRestore)
	r.Post("/api/restore/execute", h.executeRestore)
	r.Get("/api/restore/status", h.restoreStatus)
}

type handler struct {
	snapshots domain.SnapshotRepository
	restores  RestoreService
}
