package main

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// TLS and trusted-proxy wiring for `manager serve` (finding F3: no TLS
// listener blocks the encrypted-backup E2E).
//
//   - --tls-cert / --tls-key (env VAULTIX_TLS_CERT / VAULTIX_TLS_KEY): when
//     both are set the manager serves HTTPS; otherwise plain HTTP with a
//     loud WARN. Precedence: flag > env > config file > unset.
//   - VAULTIX_TRUSTED_PROXIES: comma-separated CIDRs (bare IPs allowed) of
//     reverse proxies permitted to assert X-Forwarded-Proto; empty means
//     none. Only requests from a trusted proxy IP with
//     X-Forwarded-Proto: https count as secure without direct TLS.
const (
	tlsCertKey        = "tls_cert"
	tlsKeyKey         = "tls_key"
	trustedProxiesKey = "trusted_proxies"
)

// bindTLSFlags binds the serve-local TLS flags to viper. It runs inside
// RunE next to bindRunFlags so the EXECUTING command's parsed FlagSet wins;
// un-changed flags defer to env/config per viper precedence.
func bindTLSFlags(cmd *cobra.Command) {
	if f := cmd.Flags().Lookup("tls-cert"); f != nil {
		_ = viper.BindPFlag(tlsCertKey, f)
	}
	if f := cmd.Flags().Lookup("tls-key"); f != nil {
		_ = viper.BindPFlag(tlsKeyKey, f)
	}
	_ = viper.BindEnv(trustedProxiesKey)
}

// resolveTLSFiles returns the configured TLS certificate/key paths (flag >
// env > config file). Empty strings mean "serve plain HTTP".
func resolveTLSFiles() (certFile, keyFile string) {
	return strings.TrimSpace(viper.GetString(tlsCertKey)),
		strings.TrimSpace(viper.GetString(tlsKeyKey))
}

// validateTLSFiles enforces both-or-neither and fails fast on unloadable
// pairs so a bad cert path is a loud boot error, not a listen-time one.
func validateTLSFiles(certFile, keyFile string) error {
	if (certFile == "") != (keyFile == "") {
		return fmt.Errorf("tls: --tls-cert and --tls-key (VAULTIX_TLS_CERT/VAULTIX_TLS_KEY) must be set together")
	}
	if certFile == "" {
		return nil
	}
	if _, err := tls.LoadX509KeyPair(certFile, keyFile); err != nil {
		return fmt.Errorf("tls: load key pair: %w", err)
	}
	return nil
}

// resolveTrustedProxies returns the raw VAULTIX_TRUSTED_PROXIES value (comma
// CIDRs, empty means no proxy is trusted).
func resolveTrustedProxies() string {
	return strings.TrimSpace(viper.GetString(trustedProxiesKey))
}

// logTransportMode emits the loud plain-HTTP warning (or the TLS-enabled
// confirmation) once per boot.
func logTransportMode(log *slog.Logger, certFile string) {
	if log == nil {
		log = slog.Default()
	}
	if certFile == "" {
		log.Warn("TLS disabled: serving PLAIN HTTP — set --tls-cert/--tls-key (VAULTIX_TLS_CERT/VAULTIX_TLS_KEY) for HTTPS; data-key issuance stays gated on a secure transport")
		return
	}
	log.Info("TLS enabled: serving HTTPS", "cert", certFile)
}
