package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authhttp "github.com/vaultix/vaultix/internal/adapter/http/auth"
)

// serve_test.go: cookie_secure mode resolution for `manager serve`
// (--cookie-secure flag, VAULTIX_COOKIE_SECURE env, cookie_secure config
// key; default auto). Uses resetTLSViper from tls_test.go for a
// production-shaped viper.

func TestResolveCookieSecureDefaultAuto(t *testing.T) {
	resetTLSViper(t)
	var configPath string
	bindCookieSecureFlag(newServeCommand(&configPath))
	mode, err := resolveCookieSecure()
	require.NoError(t, err)
	assert.Equal(t, authhttp.SecureAuto, mode)
}

func TestResolveCookieSecurePrecedence(t *testing.T) {
	resetTLSViper(t)
	t.Setenv("VAULTIX_COOKIE_SECURE", "off")

	// Env applies with no flags.
	var configPath string
	bindCookieSecureFlag(newServeCommand(&configPath))
	mode, err := resolveCookieSecure()
	require.NoError(t, err)
	assert.Equal(t, authhttp.SecureOff, mode)

	// Flags beat env.
	cmd := newServeCommand(&configPath)
	require.NoError(t, cmd.ParseFlags([]string{"--cookie-secure", "on"}))
	bindCookieSecureFlag(cmd)
	mode, err = resolveCookieSecure()
	require.NoError(t, err)
	assert.Equal(t, authhttp.SecureOn, mode)
}

func TestResolveCookieSecureInvalid(t *testing.T) {
	resetTLSViper(t)
	t.Setenv("VAULTIX_COOKIE_SECURE", "always")
	var configPath string
	bindCookieSecureFlag(newServeCommand(&configPath))
	_, err := resolveCookieSecure()
	require.ErrorContains(t, err, "auto|on|off", "typos must fail fast, not pick a cookie policy")
}

func TestServeHelpDocumentsCookieSecure(t *testing.T) {
	var configPath string
	cmd := newServeCommand(&configPath)
	var buf strings.Builder
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	require.NoError(t, cmd.Help())
	help := buf.String()
	for _, want := range []string{
		"--cookie-secure", "VAULTIX_COOKIE_SECURE", "auto|on|off",
	} {
		assert.True(t, strings.Contains(help, want), "serve --help must document %s", want)
	}
}
