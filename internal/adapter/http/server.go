package http

import (
	"context"
	"errors"
	"log/slog"
	nethttp "net/http"
	"time"
)

// Server is the Manager HTTP server with graceful shutdown.
type Server struct {
	Addr         string
	Handler      nethttp.Handler
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
	// ShutdownTimeout bounds graceful drain.
	ShutdownTimeout time.Duration
	Logger          *slog.Logger
}

// Run serves until ctx is cancelled, then drains gracefully. It returns nil
// on clean shutdown.
func (s Server) Run(ctx context.Context) error {
	log := s.Logger
	if log == nil {
		log = slog.Default()
	}
	shutdownTimeout := s.ShutdownTimeout
	if shutdownTimeout <= 0 {
		shutdownTimeout = 15 * time.Second
	}
	srv := &nethttp.Server{
		Addr:         s.Addr,
		Handler:      s.Handler,
		ReadTimeout:  s.ReadTimeout,
		WriteTimeout: s.WriteTimeout,
		IdleTimeout:  s.IdleTimeout,
	}
	serveErr := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", s.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, nethttp.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()
	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}
	log.Info("http server shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	return <-serveErr
}
