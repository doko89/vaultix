package http

import (
	"io/fs"
	"log/slog"
	nethttp "net/http"
	"os"
	"path"
	"strings"
)

// SPAConfig selects where the built web UI comes from.
//
// Release builds: run `npm --prefix web run build` first (Makefile
// web-build), producing web/dist/, then start the manager with Dir pointing
// at it (serve --static-dir, default ./web/dist).
//
// Why no go:embed here: go:embed patterns cannot contain `..`, and web/dist
// lives outside this package's directory (verified: `//go:embed ../...`
// fails with "invalid pattern syntax"), so the embed must live in a package
// under web/ — outside this track's scope.
//
// INTEGRATION NOTE (integrator-2): to embed the UI into the binary, add
// web/embed.go (`package web` + `//go:embed dist` + exported FS subdir) and
// pass it as SPAConfig.FS. FS wins over Dir when both are set. Dev flow is
// unchanged: `npm --prefix web run dev` (Vite on :5173, proxying /api to the
// manager — see web/vite.config.ts).
type SPAConfig struct {
	// FS serves the UI from an embedded filesystem (see note above).
	FS fs.FS
	// Dir serves the UI from disk (default "./web/dist").
	Dir string
}

// spaHandler serves the built UI with index.html fallback: unknown
// non-/api paths return index.html so client-side routing works. /api/*
// never reaches this handler (the router returns JSON 404 first).
type spaHandler struct {
	fsys fs.FS
	log  *slog.Logger
}

// newSPAHandler builds the handler. A missing dist directory does NOT fail
// construction: requests get a 503 explaining how to build the UI, so `go
// build` and API-only deployments work with web/dist absent.
func newSPAHandler(cfg SPAConfig, log *slog.Logger) *spaHandler {
	if log == nil {
		log = slog.Default()
	}
	fsys := cfg.FS
	if fsys == nil {
		dir := cfg.Dir
		if dir == "" {
			dir = "./web/dist"
		}
		fsys = os.DirFS(dir)
	}
	return &spaHandler{fsys: fsys, log: log}
}

func (h *spaHandler) ServeHTTP(w nethttp.ResponseWriter, r *nethttp.Request) {
	if r.Method != nethttp.MethodGet && r.Method != nethttp.MethodHead {
		WriteError(w, r, nethttp.StatusMethodNotAllowed, "method not allowed")
		return
	}
	p := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
	name := strings.TrimPrefix(p, "/")

	// Serve real files directly (assets, index.html itself).
	if name != "" {
		if info, err := fs.Stat(h.fsys, name); err == nil && !info.IsDir() {
			nethttp.ServeFileFS(w, r, h.fsys, name)
			return
		}
	}
	// SPA fallback: everything else renders index.html.
	if info, err := fs.Stat(h.fsys, "index.html"); err == nil && !info.IsDir() {
		nethttp.ServeFileFS(w, r, h.fsys, "index.html")
		return
	}
	h.log.Warn("web UI not built; serving 503", "path", r.URL.Path)
	WriteError(w, r, nethttp.StatusServiceUnavailable,
		"web UI not built: run `npm --prefix web run build` then restart the manager")
}
