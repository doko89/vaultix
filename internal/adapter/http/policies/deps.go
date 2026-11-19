package policies

import (
	"context"

	"github.com/vaultix/vaultix/internal/domain"
	policyuc "github.com/vaultix/vaultix/internal/usecase/policy"
	scheduleuc "github.com/vaultix/vaultix/internal/usecase/schedule"
)

// PolicyService is the policy use-case surface this package needs.
// *policyuc.Service satisfies it (asserted below); tests stub it.
type PolicyService interface {
	Create(ctx context.Context, in policyuc.CreateInput) (*domain.BackupPolicy, *domain.BackupPolicyVersion, error)
	Get(ctx context.Context, id string) (*domain.BackupPolicy, error)
	List(ctx context.Context, opts domain.ListOptions) (domain.ListResult[domain.BackupPolicy], error)
	Update(ctx context.Context, id string, in policyuc.UpdateInput) (*domain.BackupPolicy, *domain.BackupPolicyVersion, error)
	Delete(ctx context.Context, id string) error
	SetEnabled(ctx context.Context, id string, enabled bool, actor string) (*domain.BackupPolicy, error)
	PublishVersion(ctx context.Context, policyID string, in policyuc.PublishInput) (*domain.BackupPolicyVersion, error)
	GetVersion(ctx context.Context, policyID string, version int) (*domain.BackupPolicyVersion, error)
	ListVersions(ctx context.Context, policyID string) ([]domain.BackupPolicyVersion, error)
	CurrentVersion(ctx context.Context, policyID string) (*domain.BackupPolicyVersion, error)
	AddTarget(ctx context.Context, policyID string, in policyuc.TargetInput, meta policyuc.ChangeMeta) (*domain.BackupPolicyVersion, error)
	UpdateTarget(ctx context.Context, policyID, targetID string, in policyuc.TargetInput, meta policyuc.ChangeMeta) (*domain.BackupPolicyVersion, error)
	RemoveTarget(ctx context.Context, policyID, targetID string, meta policyuc.ChangeMeta) (*domain.BackupPolicyVersion, error)
}

// ScheduleService is the schedule use-case surface this package needs.
// *scheduleuc.Service satisfies it (asserted below); tests stub it.
type ScheduleService interface {
	Create(ctx context.Context, in scheduleuc.CreateInput) (*domain.Schedule, error)
	Get(ctx context.Context, id string) (*domain.Schedule, error)
	List(ctx context.Context, policyID string) ([]domain.Schedule, error)
	Update(ctx context.Context, id string, in scheduleuc.UpdateInput) (*domain.Schedule, error)
	Delete(ctx context.Context, id string) error
	Pause(ctx context.Context, id string) (*domain.Schedule, error)
	Resume(ctx context.Context, id string) (*domain.Schedule, error)
	TriggerManual(ctx context.Context, in scheduleuc.ManualTrigger) ([]*domain.BackupJob, error)
}

// Compile-time wiring guarantees: the real use-case services satisfy Deps.
var (
	_ PolicyService   = (*policyuc.Service)(nil)
	_ ScheduleService = (*scheduleuc.Service)(nil)
)

// Deps carries the package's dependencies as interfaces. Policies and
// Schedules are required. Enqueuer is the jobs track's JobEnqueuer and may
// be nil until that track lands; manual-run then answers 501. Gate is the
// jobs track's ConcurrencyGate and may be nil until that track lands;
// a nil gate admits every manual run (overlap prevention inert).
type Deps struct {
	Policies  PolicyService
	Schedules ScheduleService
	Enqueuer  scheduleuc.JobEnqueuer
	Gate      scheduleuc.ConcurrencyGate
}
