// Package http is the Manager HTTP adapter root: router assembly, server
// lifecycle, JSON responses, health, metrics, middleware, and SPA serving.
//
// Router compatibility: slices expose Mount(r <subset>, deps) where <subset>
// is a chi-compatible interface (Get/Post/Put/Patch/Delete) each slice
// defines — see the agents track's Router. This package's Mux implements
// that method set over the stdlib mux, translating chi ":id" patterns to
// stdlib "{id}" patterns, so slices Mount unchanged both today and after
// integrator-2 swaps Mux internals to *chi.Mux. The one exception is the
// jobs slice, whose Mount takes chi.Router and uses Route(): it needs real
// chi at integration (TODO(integrator-2)).
package http
