package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetTLSViper gives resolve helpers a production-shaped viper (env prefix
// + automatic env) without the config-file machinery.
func resetTLSViper(t *testing.T) {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.SetEnvPrefix("VAULTIX")
	viper.AutomaticEnv()
}

func TestResolveTLSFilesPrecedence(t *testing.T) {
	resetTLSViper(t)
	t.Setenv("VAULTIX_TLS_CERT", "/env/cert.pem")
	t.Setenv("VAULTIX_TLS_KEY", "/env/key.pem")

	// Env applies with no flags.
	var configPath string
	bindTLSFlags(newServeCommand(&configPath))
	cert, key := resolveTLSFiles()
	assert.Equal(t, "/env/cert.pem", cert)
	assert.Equal(t, "/env/key.pem", key)

	// Flags beat env.
	cmd := newServeCommand(&configPath)
	require.NoError(t, cmd.ParseFlags([]string{"--tls-cert", "/flag/cert.pem", "--tls-key", "/flag/key.pem"}))
	bindTLSFlags(cmd)
	cert, key = resolveTLSFiles()
	assert.Equal(t, "/flag/cert.pem", cert)
	assert.Equal(t, "/flag/key.pem", key)
}

func TestResolveTLSFilesDefaultEmpty(t *testing.T) {
	resetTLSViper(t)
	var configPath string
	bindTLSFlags(newServeCommand(&configPath))
	cert, key := resolveTLSFiles()
	assert.Empty(t, cert, "HTTP is the default when neither flag nor env is set")
	assert.Empty(t, key)
}

func TestResolveTrustedProxies(t *testing.T) {
	resetTLSViper(t)
	assert.Empty(t, resolveTrustedProxies(), "default trusts no proxy")
	t.Setenv("VAULTIX_TRUSTED_PROXIES", " 10.0.0.0/8,192.168.0.0/16 ")
	assert.Equal(t, "10.0.0.0/8,192.168.0.0/16", resolveTrustedProxies())
}

// writeTestKeyPair generates a self-signed cert/key pair for validate tests.
func writeTestKeyPair(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "vaultix-test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	require.NoError(t, err)
	rawKey, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	dir := t.TempDir()
	certFile = filepath.Join(dir, "test.crt")
	keyFile = filepath.Join(dir, "test.key")
	require.NoError(t, os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644))
	require.NoError(t, os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: rawKey}), 0o600))
	return certFile, keyFile
}

func TestValidateTLSFiles(t *testing.T) {
	require.NoError(t, validateTLSFiles("", ""))

	require.ErrorContains(t, validateTLSFiles("/x/cert.pem", ""), "must be set together")
	require.ErrorContains(t, validateTLSFiles("", "/x/key.pem"), "must be set together")

	cert, key := writeTestKeyPair(t)
	require.NoError(t, validateTLSFiles(cert, key))

	require.ErrorContains(t, validateTLSFiles(cert, cert), "load key pair", "cert-as-key must fail fast")
	require.ErrorContains(t, validateTLSFiles("/nonexistent/cert.pem", "/nonexistent/key.pem"), "load key pair")
}

func TestLogTransportMode(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	logTransportMode(log, "")
	assert.Contains(t, buf.String(), "WARN", "plain HTTP must log loud")
	assert.Contains(t, buf.String(), "PLAIN HTTP")

	buf.Reset()
	logTransportMode(log, "/x/cert.pem")
	assert.NotContains(t, buf.String(), "WARN")
	assert.Contains(t, buf.String(), "HTTPS")
}

func TestServeHelpDocumentsTLS(t *testing.T) {
	var configPath string
	cmd := newServeCommand(&configPath)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	require.NoError(t, cmd.Help())
	help := buf.String()
	for _, want := range []string{
		"--tls-cert", "--tls-key",
		"VAULTIX_TLS_CERT", "VAULTIX_TLS_KEY", "VAULTIX_TRUSTED_PROXIES",
	} {
		assert.True(t, strings.Contains(help, want), "serve --help must document %s", want)
	}
}
