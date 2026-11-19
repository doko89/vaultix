package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultBackOffConfig(t *testing.T) {
	b := DefaultBackOff()
	assert.Equal(t, time.Second, b.InitialInterval)
	assert.Equal(t, 2.0, b.Multiplier)
	assert.Equal(t, time.Minute, b.MaxInterval)
	assert.Equal(t, 0.5, b.RandomizationFactor)
}

func TestDefaultBackOffGrowsAndCaps(t *testing.T) {
	b := DefaultBackOff()
	b.RandomizationFactor = 0 // deterministic for the growth assertion
	first := b.NextBackOff()
	assert.Greater(t, first, time.Duration(0))
	assert.LessOrEqual(t, first, 2*time.Second)
	// Drive to the cap: intervals must never exceed MaxInterval.
	var last time.Duration
	for range 20 {
		last = b.NextBackOff()
	}
	assert.LessOrEqual(t, last, time.Minute)
	b.Reset()
	assert.LessOrEqual(t, b.NextBackOff(), 2*time.Second)
}

func testConfig() Config {
	cfg := DefaultConfig()
	cfg.ManagerURL = "https://manager.example.test"
	cfg.Token = "test-token"
	cfg.AgentID = "agent-1"
	cfg.HeartbeatInterval = 20 * time.Millisecond
	cfg.RequestTimeout = time.Second
	return cfg
}

func TestHeartbeatRunnerBeatsAndStops(t *testing.T) {
	stub := &StubManagerClient{}
	r := &HeartbeatRunner{Client: stub, Config: testConfig(), Version: "test"}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	require.NoError(t, r.Run(ctx))
	require.NotEmpty(t, stub.Heartbeats)
	assert.Equal(t, "agent-1", stub.Heartbeats[0].AgentID)
	assert.Equal(t, "test", stub.Heartbeats[0].Version)
}

func TestHeartbeatRunnerBacksOffThenRecovers(t *testing.T) {
	stub := &StubManagerClient{}
	calls := 0
	stub.OnHeartbeat = func(HeartbeatRequest) (HeartbeatResponse, error) {
		calls++
		if calls <= 2 {
			return HeartbeatResponse{}, errors.New("connection refused")
		}
		return HeartbeatResponse{OK: true}, nil
	}
	// Constant tiny backoff keeps the test fast and deterministic.
	r := &HeartbeatRunner{
		Client:     stub,
		Config:     testConfig(),
		NewBackOff: func() backoff.BackOff { return backoff.NewConstantBackOff(5 * time.Millisecond) },
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	require.NoError(t, r.Run(ctx))
	assert.GreaterOrEqual(t, calls, 3, "runner must retry through failures")
}

func TestHeartbeatRunnerValidate(t *testing.T) {
	r := &HeartbeatRunner{Config: testConfig()}
	assert.Error(t, r.Validate(), "nil client must fail")
	r.Client = &StubManagerClient{}
	assert.NoError(t, r.Validate())
}
