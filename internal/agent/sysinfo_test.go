package agent

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectHostFactsShape(t *testing.T) {
	facts := CollectHostFacts()
	assert.NotEmpty(t, facts.OS, "GOOS always known")
	assert.NotEmpty(t, facts.Arch, "GOARCH always known")
	if facts.IP != "" {
		ip := net.ParseIP(facts.IP)
		require.NotNil(t, ip, "reported IP must parse: %q", facts.IP)
		assert.NotNil(t, ip.To4(), "reported IP must be IPv4: %q", facts.IP)
		assert.False(t, ip.IsLoopback(), "reported IP must not be loopback")
	}
	if facts.DiskTotal != 0 || facts.DiskFree != 0 {
		assert.Greater(t, facts.DiskTotal, uint64(0))
		assert.LessOrEqual(t, facts.DiskFree, facts.DiskTotal)
	}
}

func TestDiskUsageRoot(t *testing.T) {
	total, free, err := diskUsage(DiskProbePath)
	require.NoError(t, err)
	assert.Greater(t, total, uint64(0))
	assert.LessOrEqual(t, free, total)
}

func TestDiskUsageMissingPath(t *testing.T) {
	_, _, err := diskUsage("/nonexistent-vaultix-probe-path")
	assert.Error(t, err)
}

func TestBeatSendsHostFacts(t *testing.T) {
	stub := &StubManagerClient{}
	r := &HeartbeatRunner{
		Client:  stub,
		Config:  testConfig(),
		Version: "test",
		HostFacts: func() HostFacts {
			return HostFacts{OS: "linux", Arch: "amd64", IP: "10.0.0.9", DiskTotal: 100, DiskFree: 40}
		},
	}
	_, err := r.Beat(context.Background())
	require.NoError(t, err)
	require.Len(t, stub.Heartbeats, 1)
	got := stub.Heartbeats[0]
	assert.Equal(t, "linux", got.OS)
	assert.Equal(t, "amd64", got.Architecture)
	assert.Equal(t, "10.0.0.9", got.IPAddress)
	assert.Equal(t, uint64(100), got.DiskTotal)
	assert.Equal(t, uint64(40), got.DiskFree)
}

func TestBeatOmitsUnknownFacts(t *testing.T) {
	stub := &StubManagerClient{}
	r := &HeartbeatRunner{
		Client:  stub,
		Config:  testConfig(),
		Version: "test",
		HostFacts: func() HostFacts {
			return HostFacts{OS: "linux", Arch: "amd64"}
		},
	}
	_, err := r.Beat(context.Background())
	require.NoError(t, err)
	require.Len(t, stub.Heartbeats, 1)
	got := stub.Heartbeats[0]
	assert.Equal(t, "", got.IPAddress)
	assert.Equal(t, uint64(0), got.DiskTotal)
	assert.Equal(t, uint64(0), got.DiskFree)
}
