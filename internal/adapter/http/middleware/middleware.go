// Package middleware holds the Manager HTTP middleware chain
// (request-ID, access logging, panic recovery, request timeout). Each
// middleware is stdlib-compatible (func(http.Handler) http.Handler), so the
// set applies unchanged whether the router is this package's stdlib-backed
// Mux or a chi router at integration.
package middleware
