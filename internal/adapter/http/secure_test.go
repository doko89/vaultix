package http

import (
	"crypto/tls"
	nethttp "net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentshttp "github.com/vaultix/vaultix/internal/adapter/http/agents"
)

// secureRequest builds a request with the given transport signals.
func secureRequest(tlsState *tls.ConnectionState, remoteAddr, forwardedProto string) *nethttp.Request {
	r := httptest.NewRequest(nethttp.MethodGet, "/api/agent/poll", nil)
	r.TLS = tlsState
	r.RemoteAddr = remoteAddr
	if forwardedProto != "" {
		r.Header.Set("X-Forwarded-Proto", forwardedProto)
	}
	return r
}

func TestIsSecureMatrix(t *testing.T) {
	directTLS := &tls.ConnectionState{}
	cases := []struct {
		name    string
		proxies string
		request *nethttp.Request
		secure  bool
	}{
		{
			name:    "direct TLS, no proxy config",
			proxies: "",
			request: secureRequest(directTLS, "9.9.9.9:1", ""),
			secure:  true,
		},
		{
			name:    "direct TLS wins even with proxies configured",
			proxies: "10.0.0.0/8",
			request: secureRequest(directTLS, "9.9.9.9:1", ""),
			secure:  true,
		},
		{
			name:    "trusted proxy with https proto",
			proxies: "10.0.0.0/8",
			request: secureRequest(nil, "10.1.2.3:9999", "https"),
			secure:  true,
		},
		{
			name:    "trusted proxy, proto case-insensitive",
			proxies: "10.0.0.0/8",
			request: secureRequest(nil, "10.1.2.3:9999", "HTTPS"),
			secure:  true,
		},
		{
			name:    "trusted bare-IP entry",
			proxies: "203.0.113.7",
			request: secureRequest(nil, "203.0.113.7:443", "https"),
			secure:  true,
		},
		{
			name:    "untrusted peer with https proto refused",
			proxies: "10.0.0.0/8",
			request: secureRequest(nil, "192.0.2.1:1", "https"),
			secure:  false,
		},
		{
			name:    "https proto with no proxies configured refused",
			proxies: "",
			request: secureRequest(nil, "10.1.2.3:9999", "https"),
			secure:  false,
		},
		{
			name:    "trusted proxy without proto header refused",
			proxies: "10.0.0.0/8",
			request: secureRequest(nil, "10.1.2.3:9999", ""),
			secure:  false,
		},
		{
			name:    "trusted proxy with http proto refused",
			proxies: "10.0.0.0/8",
			request: secureRequest(nil, "10.1.2.3:9999", "http"),
			secure:  false,
		},
		{
			name:    "plain HTTP refused",
			proxies: "",
			request: secureRequest(nil, "192.0.2.1:1", ""),
			secure:  false,
		},
		{
			name:    "unparseable peer refused",
			proxies: "0.0.0.0/0",
			request: secureRequest(nil, "not-an-addr", "https"),
			secure:  false,
		},
		{
			name:    "nil request refused",
			proxies: "0.0.0.0/0",
			request: nil,
			secure:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, agentshttp.SetTrustedProxies(tc.proxies))
			t.Cleanup(func() { _ = agentshttp.SetTrustedProxies("") })
			assert.Equal(t, tc.secure, agentshttp.IsSecure(tc.request))
		})
	}
}

func TestSetTrustedProxiesValidation(t *testing.T) {
	t.Cleanup(func() { _ = agentshttp.SetTrustedProxies("") })

	require.NoError(t, agentshttp.SetTrustedProxies(""))
	require.NoError(t, agentshttp.SetTrustedProxies("  "))
	require.NoError(t, agentshttp.SetTrustedProxies("10.0.0.0/8, 192.168.0.0/16 ,2001:db8::/32"))
	require.Error(t, agentshttp.SetTrustedProxies("not-a-cidr"))
	require.Error(t, agentshttp.SetTrustedProxies("10.0.0.0/8, bogus"))
	require.Error(t, agentshttp.SetTrustedProxies("10.0.0.0/33"))

	// A failed update leaves the previous set untouched (fail closed).
	require.NoError(t, agentshttp.SetTrustedProxies("10.0.0.0/8"))
	require.Error(t, agentshttp.SetTrustedProxies("bogus"))
	r := secureRequest(nil, "10.9.9.9:1", "https")
	assert.True(t, agentshttp.IsSecure(r), "previous trusted set must survive a bad update")
}
