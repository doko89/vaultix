package audit

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// Outcomes recorded on audit entries (subset of the DB check constraint;
// the repo maps OKSynonym "ok" and "success" identically).
const (
	OutcomeOK     = "ok"
	OutcomeDenied = "denied"
	OutcomeError  = "error"
)

// Service writes and queries audit entries. It programs against the
// domain.AuditRepository port; the repos track implements the port.
type Service struct {
	repo  domain.AuditRepository
	clock domain.Clock
}

// New returns a Service. clock may be nil (system clock is used).
func New(repo domain.AuditRepository, clock domain.Clock) (*Service, error) {
	if repo == nil {
		return nil, fmt.Errorf("audit: repository is required")
	}
	if clock == nil {
		clock = domain.SystemClock{}
	}
	return &Service{repo: repo, clock: clock}, nil
}

// Entry is the caller-facing shape for appending one audit event.
type Entry struct {
	ActorType  string
	ActorID    string
	Action     domain.AuditAction
	TargetType string
	TargetID   string
	Outcome    string
	Detail     string
	IPAddress  string
}

// Append validates, scrubs, timestamps, and persists one audit entry.
// The store is append-only: there is intentionally no update/delete path.
func (s *Service) Append(ctx context.Context, e Entry) (*domain.AuditLog, error) {
	if strings.TrimSpace(string(e.Action)) == "" {
		return nil, fmt.Errorf("%w: action is required", domain.ErrValidation)
	}
	actorType := e.ActorType
	if actorType == "" {
		actorType = "system"
	}
	switch actorType {
	case "user", "agent", "system":
	default:
		return nil, fmt.Errorf("%w: invalid actor_type %q", domain.ErrValidation, actorType)
	}
	outcome := e.Outcome
	if outcome == "" {
		outcome = OutcomeOK
	}
	switch outcome {
	case OutcomeOK, "success", OutcomeDenied, OutcomeError:
		if outcome == "success" {
			outcome = OutcomeOK
		}
	default:
		return nil, fmt.Errorf("%w: invalid outcome %q", domain.ErrValidation, outcome)
	}
	log := domain.NewAuditLog(
		s.clock.Now().UTC(),
		actorType, strings.TrimSpace(e.ActorID),
		e.Action, e.TargetType, e.TargetID,
		outcome, ScrubSecrets(e.Detail),
	)
	log.IPAddress = e.IPAddress
	if err := s.repo.Append(ctx, &log); err != nil {
		return nil, fmt.Errorf("audit append: %w", err)
	}
	return &log, nil
}

// Filter narrows audit queries. All fields are optional; zero values match
// everything. It mirrors domain.AuditFilter plus target narrowing.
type Filter struct {
	ActorID    string
	Action     domain.AuditAction
	TargetType string
	TargetID   string
	Outcome    string
	Since      *time.Time
	Until      *time.Time
}

func (f Filter) toDomain() domain.AuditFilter {
	return domain.AuditFilter{
		ActorID: f.ActorID,
		Action:  f.Action,
		Since:   f.Since,
		Until:   f.Until,
	}
}

// Query returns one page of audit entries, newest first. Adapters apply sane
// paging defaults when opts.Limit <= 0.
func (s *Service) Query(ctx context.Context, opts domain.ListOptions, f Filter) (domain.ListResult[domain.AuditLog], error) {
	res, err := s.repo.List(ctx, opts, f.toDomain())
	if err != nil {
		return domain.ListResult[domain.AuditLog]{}, fmt.Errorf("audit query: %w", err)
	}
	if f.TargetType == "" && f.TargetID == "" && f.Outcome == "" {
		return res, nil
	}
	// Target/outcome narrowing not covered by the domain filter is applied
	// in memory so every Filter field behaves identically regardless of
	// which repository implements the port.
	kept := res.Items[:0]
	for _, e := range res.Items {
		if f.TargetType != "" && e.TargetType != f.TargetType {
			continue
		}
		if f.TargetID != "" && e.TargetID != f.TargetID {
			continue
		}
		if f.Outcome != "" && e.Outcome != f.Outcome {
			continue
		}
		kept = append(kept, e)
	}
	res.Items = kept
	return res, nil
}
