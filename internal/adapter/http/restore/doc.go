// Package restore serves the backup catalog and restore workflows over HTTP:
// GET /api/catalog* (browse snapshots, manifests, encryption labels,
// restorable flags) and POST/GET /api/restore/* (plan, confirm, execute,
// status).
//
// The package is self-contained: it imports only the standard library, the
// domain, and the restore usecase. Routing goes through the local Router
// interface, which chi.Router satisfies, so the manager can mount these
// routes on its chi router without this package taking a framework
// dependency.
package restore
