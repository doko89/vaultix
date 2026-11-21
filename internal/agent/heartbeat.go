package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/cenkalti/backoff/v5"
)

// Backoff tuning for the heartbeat reconnect loop.
const (
	backoffInitialInterval = time.Second
	backoffMultiplier      = 2.0
	backoffMaxInterval     = time.Minute
	backoffRandomization   = 0.5
)

// DefaultBackOff returns the exponential backoff used between failed
// heartbeats: 1s initial, doubling, capped at 1m, with jitter.
func DefaultBackOff() *backoff.ExponentialBackOff {
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = backoffInitialInterval
	b.Multiplier = backoffMultiplier
	b.MaxInterval = backoffMaxInterval
	b.RandomizationFactor = backoffRandomization
	return b
}

// HeartbeatRunner sends periodic heartbeats and reconnects with exponential
// backoff after network interruptions.
type HeartbeatRunner struct {
	Client   ManagerClient
	Config   Config
	Hostname string
	Version  string
	Logger   *slog.Logger
	// Capabilities is the advertised set computed once at run start
	// (DetectCapabilities). Nil leaves the manager's stored set untouched;
	// non-nil replaces it, driving PollJobs capability filtering.
	Capabilities []string

	// NewBackOff builds the backoff strategy; defaults to DefaultBackOff.
	// It is a seam for deterministic tests.
	NewBackOff func() backoff.BackOff
	// HostFacts collects OS/arch/IP/disk per beat; defaults to
	// CollectHostFacts. A seam for deterministic tests.
	HostFacts func() HostFacts
}

func (r *HeartbeatRunner) logger() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}

func (r *HeartbeatRunner) newBackOff() backoff.BackOff {
	if r.NewBackOff != nil {
		return r.NewBackOff()
	}
	return DefaultBackOff()
}

// Beat sends a single heartbeat with the runner's request timeout.
func (r *HeartbeatRunner) Beat(ctx context.Context) (HeartbeatResponse, error) {
	hostname := r.Hostname
	if hostname == "" {
		hostname, _ = os.Hostname()
	}
	collect := r.HostFacts
	if collect == nil {
		collect = CollectHostFacts
	}
	facts := collect()
	ctx, cancel := context.WithTimeout(ctx, r.Config.RequestTimeout)
	defer cancel()
	return r.Client.Heartbeat(ctx, HeartbeatRequest{
		AgentID:         r.Config.AgentID,
		Hostname:        hostname,
		Version:         r.Version,
		OS:              facts.OS,
		Architecture:    facts.Arch,
		Capabilities:    r.Capabilities,
		DiskTotal:       facts.DiskTotal,
		DiskFree:        facts.DiskFree,
		IPAddress:       facts.IP,
		DockerAvailable: r.Config.DockerEnabled,
		At:              time.Now().UTC(),
	})
}

// Run loops until ctx is cancelled: heartbeat every interval, and on failure
// retry with exponential backoff until the Manager is reachable again.
// It returns nil on clean shutdown.
func (r *HeartbeatRunner) Run(ctx context.Context) error {
	log := r.logger()
	bo := r.newBackOff()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		resp, err := r.Beat(ctx)
		if err == nil {
			bo.Reset()
			log.Debug("heartbeat ok", "jobs_pending", resp.JobsPending)
			timer.Reset(r.Config.HeartbeatInterval)
			continue
		}
		wait := bo.NextBackOff()
		// Never log the credential: err comes from the client, which only
		// formats method, URL, and status — never the Authorization header.
		log.Warn("heartbeat failed, backing off",
			"error", err, "retry_in", wait.String())
		timer.Reset(wait)
	}
}

// Ensure the runner fails fast on programmer error.
func (r *HeartbeatRunner) Validate() error {
	if r.Client == nil {
		return fmt.Errorf("agent: heartbeat runner requires a ManagerClient")
	}
	return r.Config.Validate()
}
