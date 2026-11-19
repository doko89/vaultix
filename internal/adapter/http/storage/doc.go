// Package storage serves the storage HTTP API: destination CRUD with
// connection testing under /api/storage and secret-profile CRUD with
// metadata-only reads under /api/credentials.
//
// The package is self-contained: it programs against its local Service
// interface (satisfied by the storage use case with zero glue) and imports
// no sibling HTTP packages. Mount accepts the Router subset of chi.Router,
// so once github.com/go-chi/chi/v5 is added to go.mod a *chi.Mux can be
// passed directly; NewMux serves the same routes on the stdlib multiplexer.
package storage
