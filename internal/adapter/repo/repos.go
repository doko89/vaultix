package repo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vaultix/vaultix/db/gen"
	"github.com/vaultix/vaultix/internal/domain"
)

// DefaultQueryTimeout bounds every repository DB call that arrives without
// a caller-set deadline.
const DefaultQueryTimeout = 5 * time.Second

// Repos bundles every repository port implementation plus the concrete-only
// stores (policy targets via PolicyRepo, retention via RetentionStore).
// Construct it once in cmd wiring with New and pass fields as domain ports.
type Repos struct {
	Users         *UserRepo
	OIDC          *OIDCRepo
	Agents        *AgentRepo
	Discovery     *DiscoveryRepo
	Storage       *StorageRepo
	Policies      *PolicyRepo
	Retention     *RetentionStore
	Schedules     *ScheduleRepo
	BackupJobs    *BackupJobRepo
	RestoreJobs   *RestoreJobRepo
	Snapshots     *SnapshotRepo
	Stacks        *StackRepo
	Notifications *NotificationRepo
	Audit         *AuditRepo
}

// New builds the repository bundle on the shared pool. The pool must be
// configured (non-nil); queries run through sqlc-generated code only.
func New(pool *pgxpool.Pool) *Repos {
	q := gen.New(pool)
	base := baseRepo{pool: pool, q: q}
	return &Repos{
		Users:         &UserRepo{baseRepo: base},
		OIDC:          &OIDCRepo{baseRepo: base},
		Agents:        &AgentRepo{baseRepo: base},
		Discovery:     &DiscoveryRepo{baseRepo: base},
		Storage:       &StorageRepo{baseRepo: base},
		Policies:      &PolicyRepo{baseRepo: base},
		Retention:     &RetentionStore{baseRepo: base},
		Schedules:     &ScheduleRepo{baseRepo: base},
		BackupJobs:    &BackupJobRepo{baseRepo: base},
		RestoreJobs:   &RestoreJobRepo{baseRepo: base},
		Snapshots:     &SnapshotRepo{baseRepo: base},
		Stacks:        newStackRepo(),
		Notifications: &NotificationRepo{baseRepo: base},
		Audit:         &AuditRepo{baseRepo: base},
	}
}

// Compile-time port assertions: the bundle must satisfy every repository
// port in domain/ports.go.
var (
	_ domain.UserRepository         = (*UserRepo)(nil)
	_ domain.OIDCProviderRepository = (*OIDCRepo)(nil)
	_ domain.AgentRepository        = (*AgentRepo)(nil)
	_ domain.DiscoveryRepository    = (*DiscoveryRepo)(nil)
	_ domain.StorageRepository      = (*StorageRepo)(nil)
	_ domain.PolicyRepository       = (*PolicyRepo)(nil)
	_ domain.ScheduleRepository     = (*ScheduleRepo)(nil)
	_ domain.BackupJobRepository    = (*BackupJobRepo)(nil)
	_ domain.RestoreJobRepository   = (*RestoreJobRepo)(nil)
	_ domain.SnapshotRepository     = (*SnapshotRepo)(nil)
	_ domain.StackRepository        = (*StackRepo)(nil)
	_ domain.NotificationRepository = (*NotificationRepo)(nil)
	_ domain.AuditRepository        = (*AuditRepo)(nil)
)

// baseRepo shares the pool (for transactions) and the pool-bound queries.
type baseRepo struct {
	pool *pgxpool.Pool
	q    *gen.Queries
}

// withTimeout applies DefaultQueryTimeout unless the caller already set a
// deadline. Callers must defer the cancel func.
func withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, DefaultQueryTimeout)
}

// inTx runs fn inside a pgx transaction, rolling back on error or panic.
// On success the transaction commits; fn receives queries bound to the tx.
func (b baseRepo) inTx(ctx context.Context, fn func(q *gen.Queries) error) error {
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx) // no-op after successful commit
	}()
	if err := fn(b.q.WithTx(tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
