package scheduler

import "time"

// RetryPolicy bounds attempts and spaces retries with exponential backoff.
//
// River mapping (integrator-2): MaxAttempts maps onto river InsertOpts
// (MaxAttempts) per job kind; the backoff curve maps onto the River client's
// retry/backoff hook. The in-process runner below applies the same policy,
// so behavior observed in tests matches production.
type RetryPolicy struct {
	// MaxAttempts caps total tries including the first (1 = no retry).
	MaxAttempts int
	// InitialInterval is the delay before the second attempt.
	InitialInterval time.Duration
	// MaxInterval caps the delay between attempts.
	MaxInterval time.Duration
	// Multiplier grows the delay per attempt (2 = doubling).
	Multiplier float64
}

// RetryPolicyFor returns the policy for a job kind (see Kind methods in
// jobs.go). Unknown kinds get the conservative default.
//
// Rationale: schedule dispatches are cheap and idempotent-ish (a duplicate
// dispatch is deduplicated by the jobs service), so backup-execution retries
// generously; retention deletes data, so it retries narrowly and surfaces
// failures to operators instead of hammering storage.
func RetryPolicyFor(kind string) RetryPolicy {
	switch kind {
	case "backup-execution":
		return RetryPolicy{MaxAttempts: 5, InitialInterval: 30 * time.Second, MaxInterval: 15 * time.Minute, Multiplier: 2}
	case "retention":
		return RetryPolicy{MaxAttempts: 3, InitialInterval: time.Minute, MaxInterval: 30 * time.Minute, Multiplier: 2}
	default:
		return RetryPolicy{MaxAttempts: 3, InitialInterval: 30 * time.Second, MaxInterval: 10 * time.Minute, Multiplier: 2}
	}
}

// BackoffForAttempt returns the delay before attempt n (1-based: attempt 1
// is the first try, so BackoffForAttempt(p, 1) is the delay before retry #1).
// attempt <= 1 returns InitialInterval; growth is exponential in Multiplier
// capped at MaxInterval.
func BackoffForAttempt(p RetryPolicy, attempt int) time.Duration {
	if p.InitialInterval <= 0 {
		return 0
	}
	mult := p.Multiplier
	if mult < 1 {
		mult = 1
	}
	d := float64(p.InitialInterval)
	for i := 1; i < attempt; i++ {
		d *= mult
		if p.MaxInterval > 0 && d >= float64(p.MaxInterval) {
			return p.MaxInterval
		}
	}
	if p.MaxInterval > 0 && d > float64(p.MaxInterval) {
		return p.MaxInterval
	}
	return time.Duration(d)
}
