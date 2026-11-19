package http

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	nethttp "net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeSelfSignedCert generates a test-only self-signed CA cert for
// 127.0.0.1/localhost and writes the cert+key PEM files, returning their
// paths plus a pool trusting the cert.
func writeSelfSignedCert(t *testing.T) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "vaultix-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
	}
	tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	require.NoError(t, err)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	rawKey, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: rawKey})
	dir := t.TempDir()
	certFile = filepath.Join(dir, "test.crt")
	keyFile = filepath.Join(dir, "test.key")
	require.NoError(t, os.WriteFile(certFile, certPEM, 0o644))
	require.NoError(t, os.WriteFile(keyFile, keyPEM, 0o600))
	pool = x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(certPEM))
	return certFile, keyFile, pool
}

func pingMux() *nethttp.ServeMux {
	mux := nethttp.NewServeMux()
	mux.HandleFunc("/ping", func(w nethttp.ResponseWriter, r *nethttp.Request) {
		_, _ = w.Write([]byte("pong"))
	})
	return mux
}

func TestServerTLSBoot(t *testing.T) {
	certFile, keyFile, pool := writeSelfSignedCert(t)
	addr := "127.0.0.1:18084"
	s := Server{
		Addr: addr, Handler: pingMux(),
		CertFile: certFile, KeyFile: keyFile,
		Logger: discardLogger(), ShutdownTimeout: 5 * time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	client := &nethttp.Client{Transport: &nethttp.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
	}}
	var resp *nethttp.Response
	require.Eventually(t, func() bool {
		var err error
		resp, err = client.Get("https://" + addr + "/ping") //nolint:gosec,noctx // test-local probe, pinned trust pool
		if err != nil {
			return false
		}
		return resp.StatusCode == nethttp.StatusOK
	}, 5*time.Second, 10*time.Millisecond)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "pong", string(body))
	require.NotNil(t, resp.TLS)
	assert.GreaterOrEqual(t, resp.TLS.Version, uint16(tls.VersionTLS12), "must negotiate TLS 1.2+")

	// Plain HTTP against the TLS port must not get a served response
	// (Go answers 400 "Client sent an HTTP request to an HTTPS server").
	plain := &nethttp.Client{Transport: &nethttp.Transport{Proxy: nil}}
	plainResp, err := plain.Get("http://" + addr + "/ping") //nolint:gosec,noctx // test-local negative probe
	if err == nil {
		defer func() { _ = plainResp.Body.Close() }()
		assert.Equal(t, nethttp.StatusBadRequest, plainResp.StatusCode)
	}

	// Graceful shutdown is preserved on the TLS path.
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("TLS server did not shut down gracefully")
	}
}

func TestServerHTTPDefault(t *testing.T) {
	addr := "127.0.0.1:18085"
	s := Server{Addr: addr, Handler: pingMux(), Logger: discardLogger(), ShutdownTimeout: 5 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	var resp *nethttp.Response
	require.Eventually(t, func() bool {
		var err error
		resp, err = nethttp.Get("http://" + addr + "/ping") //nolint:gosec,noctx // test-local probe
		if err != nil {
			return false
		}
		return resp.StatusCode == nethttp.StatusOK
	}, 5*time.Second, 10*time.Millisecond)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, nethttp.StatusOK, resp.StatusCode)
	assert.Nil(t, resp.TLS, "default server speaks plain HTTP")

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down gracefully")
	}
}

func TestServerTLSHalfConfigured(t *testing.T) {
	certFile, keyFile, _ := writeSelfSignedCert(t)
	ctx := context.Background()
	err := Server{Addr: "127.0.0.1:18086", Handler: pingMux(), CertFile: certFile}.Run(ctx)
	require.ErrorContains(t, err, "both CertFile and KeyFile")
	err = Server{Addr: "127.0.0.1:18086", Handler: pingMux(), KeyFile: keyFile}.Run(ctx)
	require.ErrorContains(t, err, "both CertFile and KeyFile")
}
