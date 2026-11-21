package discovery

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time { return c.now }

type fakeDiscoveryRepo struct {
	mu        sync.Mutex
	byAgent   map[string][]domain.DiscoveredResource
	byID      map[string]domain.DiscoveredResource
	replaces  int
	lastAgent string
}

func newFakeDiscoveryRepo() *fakeDiscoveryRepo {
	return &fakeDiscoveryRepo{
		byAgent: map[string][]domain.DiscoveredResource{},
		byID:    map[string]domain.DiscoveredResource{},
	}
}

func (f *fakeDiscoveryRepo) ReplaceAgentResources(_ context.Context, agentID string, resources []domain.DiscoveredResource) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, old := range f.byAgent[agentID] {
		delete(f.byID, old.ID)
	}
	cp := append([]domain.DiscoveredResource(nil), resources...)
	f.byAgent[agentID] = cp
	for _, r := range cp {
		f.byID[r.ID] = r
	}
	f.replaces++
	f.lastAgent = agentID
	return nil
}

func (f *fakeDiscoveryRepo) ListAgentResources(_ context.Context, agentID string, kind domain.ResourceKind) ([]domain.DiscoveredResource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.DiscoveredResource
	for _, r := range f.byAgent[agentID] {
		if kind != "" && r.Kind != kind {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeDiscoveryRepo) GetResource(_ context.Context, id string) (*domain.DiscoveredResource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.byID[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := r
	return &cp, nil
}

type fakeAgentRepo struct {
	mu     sync.Mutex
	agents map[string]*domain.Agent
}

func (f *fakeAgentRepo) CreateAgent(_ context.Context, a *domain.Agent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *a
	f.agents[a.ID] = &cp
	return nil
}

func (f *fakeAgentRepo) GetAgent(_ context.Context, id string) (*domain.Agent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.agents[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *a
	return &cp, nil
}

func (f *fakeAgentRepo) UpdateAgent(_ context.Context, a *domain.Agent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.agents[a.ID]; !ok {
		return domain.ErrNotFound
	}
	cp := *a
	f.agents[a.ID] = &cp
	return nil
}

func (f *fakeAgentRepo) DeleteAgent(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.agents[id]; !ok {
		return domain.ErrNotFound
	}
	delete(f.agents, id)
	return nil
}

func (f *fakeAgentRepo) ListAgents(_ context.Context, _ domain.ListOptions) (domain.ListResult[domain.Agent], error) {
	return domain.ListResult[domain.Agent]{}, nil
}

func (f *fakeAgentRepo) StoreCredential(_ context.Context, _ *domain.AgentCredential) error {
	return nil
}

func (f *fakeAgentRepo) GetCredentialByHash(_ context.Context, _ string) (*domain.AgentCredential, error) {
	return nil, domain.ErrNotFound
}

func (f *fakeAgentRepo) ListAgentCredentials(_ context.Context, _ string) ([]domain.AgentCredential, error) {
	return nil, nil
}

func (f *fakeAgentRepo) UpdateCredential(_ context.Context, _ *domain.AgentCredential) error {
	return nil
}

func (f *fakeAgentRepo) RecordHeartbeat(_ context.Context, _ *domain.AgentHeartbeat) error {
	return nil
}

func (f *fakeAgentRepo) LatestHeartbeat(_ context.Context, _ string) (*domain.AgentHeartbeat, error) {
	return nil, domain.ErrNotFound
}

func testSetup() (*Service, *fakeDiscoveryRepo) {
	disco := newFakeDiscoveryRepo()
	agents := &fakeAgentRepo{agents: map[string]*domain.Agent{
		"ag-1": {ID: "ag-1", Hostname: "vm-01"},
	}}
	n := 0
	svc, err := NewService(disco, agents, &fakeClock{now: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)},
		WithIDGenerator(func() (string, error) {
			n++
			return fmt.Sprintf("res-%d", n), nil
		}),
	)
	if err != nil {
		panic(err)
	}
	return svc, disco
}

func TestNewServiceRequiresRepos(t *testing.T) {
	if _, err := NewService(nil, &fakeAgentRepo{}, nil); err == nil {
		t.Fatal("want error for nil DiscoveryRepository")
	}
	if _, err := NewService(newFakeDiscoveryRepo(), nil, nil); err == nil {
		t.Fatal("want error for nil AgentRepository")
	}
}

func TestIngestAndQuery(t *testing.T) {
	svc, disco := testSetup()
	ctx := context.Background()

	n, err := svc.Ingest(ctx, "ag-1", []ResourceInput{
		{Kind: domain.ResourceKindHostPath, Name: "/srv/data"},
		{Kind: domain.ResourceKindMount, Name: "/", Detail: map[string]string{DetailFilesystem: "ext4"}},
		{
			Kind: domain.ResourceKindDockerContainer,
			Name: "web",
			Detail: map[string]string{
				DetailContainerID: "abc123",
				DetailImage:       "nginx:1.27",
				DetailState:       "running",
				DetailMounts:      `[{"type":"volume","name":"webdata","destination":"/data"}]`,
				LabelPrefix + "com.docker.compose.project": "shop",
			},
		},
		{Kind: domain.ResourceKindDockerVolume, Name: "webdata", Detail: map[string]string{DetailVolumeDriver: "local"}},
		{Kind: domain.ResourceKindComposeProject, Name: "shop", Detail: map[string]string{DetailComposeFile: "compose.yml"}},
	})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if n != 5 || disco.replaces != 1 || disco.lastAgent != "ag-1" {
		t.Fatalf("ingest result: n=%d replaces=%d agent=%q", n, disco.replaces, disco.lastAgent)
	}

	all, err := svc.List(ctx, "ag-1", "")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("want 5 resources, got %d", len(all))
	}
	ctrs, err := svc.List(ctx, "ag-1", domain.ResourceKindDockerContainer)
	if err != nil {
		t.Fatalf("list containers: %v", err)
	}
	if len(ctrs) != 1 || ctrs[0].Detail[DetailContainerID] != "abc123" || ctrs[0].Detail[DetailImage] != "nginx:1.27" {
		t.Fatalf("container detail lost: %+v", ctrs)
	}
	if ctrs[0].Detail[LabelPrefix+"com.docker.compose.project"] != "shop" {
		t.Fatalf("labels lost: %+v", ctrs[0].Detail)
	}

	got, err := svc.Get(ctx, ctrs[0].ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "web" {
		t.Fatalf("want web, got %q", got.Name)
	}

	// Second ingest replaces the snapshot (informational inventory, no merge).
	if _, err := svc.Ingest(ctx, "ag-1", []ResourceInput{
		{Kind: domain.ResourceKindHostPath, Name: "/srv/only"},
	}); err != nil {
		t.Fatalf("re-ingest: %v", err)
	}
	after, _ := svc.List(ctx, "ag-1", "")
	if len(after) != 1 || after[0].Name != "/srv/only" {
		t.Fatalf("want replaced snapshot, got %+v", after)
	}
	if _, err := svc.Get(ctx, ctrs[0].ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("stale resource: want ErrNotFound, got %v", err)
	}
}

func TestIngestValidation(t *testing.T) {
	svc, disco := testSetup()
	ctx := context.Background()

	if _, err := svc.Ingest(ctx, "", nil); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("blank agent: want ErrValidation, got %v", err)
	}
	if _, err := svc.Ingest(ctx, "missing", nil); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing agent: want ErrNotFound, got %v", err)
	}
	if _, err := svc.Ingest(ctx, "ag-1", []ResourceInput{{Kind: "nope", Name: "x"}}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("bad kind: want ErrValidation, got %v", err)
	}
	if _, err := svc.Ingest(ctx, "ag-1", []ResourceInput{{Kind: domain.ResourceKindHostPath}}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("blank name: want ErrValidation, got %v", err)
	}
	if disco.replaces != 0 {
		t.Fatalf("rejected ingest must not touch the repo, replaces=%d", disco.replaces)
	}

	// Empty ingest is explicit: it clears the snapshot.
	if _, err := svc.Ingest(ctx, "ag-1", []ResourceInput{{Kind: domain.ResourceKindHostPath, Name: "/x"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	n, err := svc.Ingest(ctx, "ag-1", nil)
	if err != nil || n != 0 {
		t.Fatalf("clear: n=%d err=%v", n, err)
	}
	after, _ := svc.List(ctx, "ag-1", "")
	if len(after) != 0 {
		t.Fatalf("want cleared snapshot, got %+v", after)
	}
}

func TestListValidation(t *testing.T) {
	svc, _ := testSetup()
	ctx := context.Background()
	if _, err := svc.List(ctx, "", ""); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("blank agent: want ErrValidation, got %v", err)
	}
	if _, err := svc.List(ctx, "ag-1", "bogus"); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("bad kind: want ErrValidation, got %v", err)
	}
	if _, err := svc.List(ctx, "missing", ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing agent: want ErrNotFound, got %v", err)
	}
	if _, err := svc.Get(ctx, ""); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("blank id: want ErrValidation, got %v", err)
	}
	if _, err := svc.Get(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing id: want ErrNotFound, got %v", err)
	}
}

func TestValidKind(t *testing.T) {
	for _, k := range []domain.ResourceKind{
		domain.ResourceKindHostPath, domain.ResourceKindMount,
		domain.ResourceKindDockerContainer, domain.ResourceKindDockerVolume,
		domain.ResourceKindDockerBindMount, domain.ResourceKindComposeProject,
		domain.ResourceKindPostgresDatabase, domain.ResourceKindMySQLDatabase,
		domain.ResourceKindMSSQLDatabase, domain.ResourceKindRedisDataset,
	} {
		if !ValidKind(k) {
			t.Fatalf("kind %q should be valid", k)
		}
	}
	if ValidKind("terraform_state") {
		t.Fatal("unknown kind should be invalid")
	}
}
