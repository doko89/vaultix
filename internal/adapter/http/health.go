package http

import (
	"context"
	"log/slog"
	nethttp "net/http"
	"time"
)

// CheckStatus is one health-check outcome.
type CheckStatus string

const (
	CheckOK            CheckStatus = "ok"
	CheckFail          CheckStatus = "fail"
	CheckNotConfigured CheckStatus = "not_configured"
)

// CheckResult is the JSON shape of one check.
type CheckResult struct {
	Status  CheckStatus `json:"status"`
	Message string      `json:"message,omitempty"`
	Latency string      `json:"latency,omitempty"`
}

// HealthResponse is the /healthz JSON shape.
type HealthResponse struct {
	Status  string                 `json:"status"` // ok | degraded | down
	Version string                 `json:"version,omitempty"`
	Checks  map[string]CheckResult `json:"checks"`
}

// DBPinger is the database health seam. *pgxpool.Pool satisfies it.
type DBPinger interface {
	Ping(ctx context.Context) error
}

// QueueChecker is the job-queue health seam.
//
// INTEGRATION NOTE (river): *scheduler.Scheduler (this track) satisfies this
// interface today via its Health method; when integrator-2 swaps the
// in-process runner for a river.Client adapter, the adapter keeps the same
// method so this seam is unchanged.
type QueueChecker interface {
	Health(ctx context.Context) error
}

// HealthDeps wires the health handler. DB is required; Queue may be nil
// (reported as not_configured, neutral to the overall status); S3Configured
// reports whether object-storage config is present (informational only —
// storage is configured post-install via the storage API, so it never fails
// health).
type HealthDeps struct {
	DB           DBPinger
	Queue        QueueChecker
	S3Configured bool
	Version      string
	Logger       *slog.Logger
}

// checkTimeout bounds each individual health check.
const checkTimeout = 2 * time.Second

// CheckHealth evaluates every check and derives the overall status:
// "down" when the database is unreachable (nothing can be served), "ok"
// otherwise. A failing queue is reported per-check but does not take the
// process out of rotation on its own — the API stays servable while the
// scheduler is repaired.
func CheckHealth(ctx context.Context, deps HealthDeps) (HealthResponse, int) {
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	res := HealthResponse{Version: deps.Version, Checks: map[string]CheckResult{}}

	res.Checks["db"] = runCheck(ctx, "db", deps.DB != nil, func(ctx context.Context) error {
		return deps.DB.Ping(ctx)
	})
	if deps.Queue == nil {
		res.Checks["queue"] = CheckResult{Status: CheckNotConfigured, Message: "scheduler not wired"}
	} else {
		res.Checks["queue"] = runCheck(ctx, "queue", true, deps.Queue.Health)
	}
	if deps.S3Configured {
		res.Checks["s3"] = CheckResult{Status: CheckOK}
	} else {
		res.Checks["s3"] = CheckResult{Status: CheckNotConfigured, Message: "object storage not configured"}
	}

	switch {
	case res.Checks["db"].Status != CheckOK:
		res.Status = "down"
		return res, nethttp.StatusServiceUnavailable
	case res.Checks["queue"].Status == CheckFail:
		res.Status = "degraded"
		return res, nethttp.StatusOK
	default:
		res.Status = "ok"
		return res, nethttp.StatusOK
	}
}

func runCheck(ctx context.Context, name string, configured bool, fn func(context.Context) error) CheckResult {
	if !configured {
		return CheckResult{Status: CheckNotConfigured}
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	if err := fn(ctx); err != nil {
		return CheckResult{Status: CheckFail, Message: err.Error(), Latency: time.Since(start).String()}
	}
	return CheckResult{Status: CheckOK, Latency: time.Since(start).String()}
}
