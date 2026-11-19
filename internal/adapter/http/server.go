package http

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"strings"
	"time"
)

// Server is the Manager HTTP server with graceful shutdown. When CertFile
// and KeyFile are both set it serves HTTPS (TLS 1.2+); setting exactly one
// is a boot error; with neither it serves plain HTTP as before.
type Server struct {
	Addr         string
	Handler      nethttp.Handler
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration
	// ShutdownTimeout bounds graceful drain.
	ShutdownTimeout time.Duration
	// CertFile/KeyFile are the PEM certificate and private key for HTTPS.
	// Both or neither; paths are passed to ListenAndServeTLS.
	CertFile string
	KeyFile  string
	Logger   *slog.Logger
}

// defaultTLSConfig enforces TLS 1.2+ with ECDHE+AEAD suites (TLS 1.3 suites
// are always AEAD and not configurable). HTTP/2 stays auto-negotiated via
// Go's default NextProto handling.
func defaultTLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
		},
		CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256},
	}
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
	certFile, keyFile := strings.TrimSpace(s.CertFile), strings.TrimSpace(s.KeyFile)
	if (certFile == "") != (keyFile == "") {
		return fmt.Errorf("http: tls requires both CertFile and KeyFile")
	}
	tlsOn := certFile != ""
	srv := &nethttp.Server{
		Addr:         s.Addr,
		Handler:      s.Handler,
		ReadTimeout:  s.ReadTimeout,
		WriteTimeout: s.WriteTimeout,
		IdleTimeout:  s.IdleTimeout,
	}
	if tlsOn {
		srv.TLSConfig = defaultTLSConfig()
	}
	serveErr := make(chan error, 1)
	go func() {
		var err error
		if tlsOn {
			log.Info("https server listening", "addr", s.Addr)
			err = srv.ListenAndServeTLS(certFile, keyFile)
		} else {
			log.Info("http server listening", "addr", s.Addr)
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, nethttp.ErrServerClosed) {
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
