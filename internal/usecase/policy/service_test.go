package policy

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

// ---- Fakes (copy semantics, like a SQL adapter) ----

type fakePolicies struct {
	policies map[string]*domain.BackupPolicy
	versions map[string][]*domain.BackupPolicyVersion
}

func newFakePolicies() *fakePolicies {
	return &fakePolicies{policies: map[string]*domain.BackupPolicy{}, versions: map[string][]*domain.BackupPolicyVersion{}}
}

func (f *fakePolicies) CreatePolicy(_ context.Context, p *domain.BackupPolicy) error {
	cp := *p
	cp.AgentIDs = append([]string{}, p.AgentIDs...)
	f.policies[p.ID] = &cp
	return nil
}

func (f *fakePolicies) GetPolicy(_ context.Context, id string) (*domain.BackupPolicy, error) {
	p, ok := f.policies[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *p
	cp.AgentIDs = append([]string{}, p.AgentIDs...)
	return &cp, nil
}

func (f *fakePolicies) UpdatePolicy(_ context.Context, p *domain.BackupPolicy) error {
	if _, ok := f.policies[p.ID]; !ok {
		return domain.ErrNotFound
	}
	cp := *p
	cp.AgentIDs = append([]string{}, p.AgentIDs...)
	f.policies[p.ID] = &cp
	return nil
}

func (f *fakePolicies) DeletePolicy(_ context.Context, id string) error {
	if _, ok := f.policies[id]; !ok {
		return domain.ErrNotFound
	}
	delete(f.policies, id)
	return nil
}

func (f *fakePolicies) ListPolicies(_ context.Context, _ domain.ListOptions) (domain.ListResult[domain.BackupPolicy], error) {
	out := domain.ListResult[domain.BackupPolicy]{}
	for _, p := range f.policies {
		cp := *p
		out.Items = append(out.Items, cp)
	}
	out.Total = len(out.Items)
	return out, nil
}

func (f *fakePolicies) PublishVersion(_ context.Context, v *domain.BackupPolicyVersion) error {
	cp := *v
	cp.Targets = cloneTargets(v.Targets)
	f.versions[v.PolicyID] = append(f.versions[v.PolicyID], &cp)
	return nil
}

func (f *fakePolicies) GetVersion(_ context.Context, policyID string, version int) (*domain.BackupPolicyVersion, error) {
	for _, v := range f.versions[policyID] {
		if v.Version == version {
			cp := *v
			cp.Targets = cloneTargets(v.Targets)
			return &cp, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (f *fakePolicies) ListVersions(_ context.Context, policyID string) ([]domain.BackupPolicyVersion, error) {
	var out []domain.BackupPolicyVersion
	for _, v := range f.versions[policyID] {
		cp := *v
		cp.Targets = cloneTargets(v.Targets)
		out = append(out, cp)
	}
	return out, nil
}

type fakeStorage struct {
	dests   map[string]*domain.StorageDestination
	secrets map[string]*domain.SecretProfile
}

func (f *fakeStorage) CreateDestination(_ context.Context, d *domain.StorageDestination) error {
	f.dests[d.ID] = d
	return nil
}

func (f *fakeStorage) GetDestination(_ context.Context, id string) (*domain.StorageDestination, error) {
	d, ok := f.dests[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return d, nil
}

func (f *fakeStorage) GetDefaultDestination(_ context.Context) (*domain.StorageDestination, error) {
	return nil, domain.ErrNotFound
}

func (f *fakeStorage) UpdateDestination(_ context.Context, d *domain.StorageDestination) error {
	f.dests[d.ID] = d
	return nil
}

func (f *fakeStorage) DeleteDestination(_ context.Context, id string) error {
	delete(f.dests, id)
	return nil
}

func (f *fakeStorage) ListDestinations(_ context.Context) ([]domain.StorageDestination, error) {
	return nil, nil
}

func (f *fakeStorage) CreateSecret(_ context.Context, s *domain.SecretProfile) error {
	f.secrets[s.ID] = s
	return nil
}

func (f *fakeStorage) GetSecret(_ context.Context, id string) (*domain.SecretProfile, error) {
	s, ok := f.secrets[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return s, nil
}

func (f *fakeStorage) DeleteSecret(_ context.Context, id string) error {
	delete(f.secrets, id)
	return nil
}

func (f *fakeStorage) ListSecrets(_ context.Context) ([]domain.SecretProfile, error) {
	return nil, nil
}

type fakeStacks struct {
	stacks    map[string]*domain.ApplicationStack
	resources map[string][]domain.ApplicationStackResource
}

func (f *fakeStacks) CreateStack(_ context.Context, s *domain.ApplicationStack) error {
	f.stacks[s.ID] = s
	return nil
}

func (f *fakeStacks) GetStack(_ context.Context, id string) (*domain.ApplicationStack, error) {
	s, ok := f.stacks[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return s, nil
}

func (f *fakeStacks) UpdateStack(_ context.Context, s *domain.ApplicationStack) error {
	f.stacks[s.ID] = s
	return nil
}

func (f *fakeStacks) DeleteStack(_ context.Context, id string) error {
	delete(f.stacks, id)
	return nil
}

func (f *fakeStacks) ListStacks(_ context.Context, _ string) ([]domain.ApplicationStack, error) {
	return nil, nil
}

func (f *fakeStacks) SetStackResources(_ context.Context, stackID string, r []domain.ApplicationStackResource) error {
	f.resources[stackID] = r
	return nil
}

func (f *fakeStacks) ListStackResources(_ context.Context, stackID string) ([]domain.ApplicationStackResource, error) {
	return f.resources[stackID], nil
}

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

// ---- Helpers ----

func testRetention() domain.RetentionRule {
	return domain.RetentionRule{KeepLastN: 7, KeepMinimum: 1}
}

func hostTarget(sel string) TargetInput {
	return TargetInput{Type: domain.TargetHostFilesystem, Selector: sel}
}

func testService() (*Service, *fakePolicies) {
	fp := newFakePolicies()
	return NewService(fp, WithClock(fixedClock{now: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)})), fp
}

func mustCreate(t *testing.T, s *Service, mutate func(*CreateInput)) (*domain.BackupPolicy, *domain.BackupPolicyVersion) {
	t.Helper()
	in := CreateInput{
		Name:      "daily-home",
		AgentIDs:  []string{"ag_1"},
		Retention: testRetention(),
		Targets:   []TargetInput{hostTarget("/var/lib/app")},
		CreatedBy: "admin",
	}
	if mutate != nil {
		mutate(&in)
	}
	p, v, err := s.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("create failed: %v", err)
	}
	return p, v
}

// ---- Tests ----

func TestCreatePublishesV1(t *testing.T) {
	s, _ := testService()
	p, v := mustCreate(t, s, nil)
	if p.CurrentVersion != 1 || v.Version != 1 {
		t.Fatalf("want v1, got policy=%d version=%d", p.CurrentVersion, v.Version)
	}
	if !p.Enabled {
		t.Fatal("new policy must be enabled")
	}
	if v.Targets[0].Method != domain.BackupMethodFull {
		t.Fatalf("host default method must be full, got %q", v.Targets[0].Method)
	}
	if v.Targets[0].ID == "" || v.Targets[0].PolicyID != p.ID {
		t.Fatal("targets must carry IDs and the policy ID")
	}
	// Caller-side mutation after create must not corrupt the stored version.
	if _, err := s.GetVersion(context.Background(), p.ID, 1); err != nil {
		t.Fatalf("get v1: %v", err)
	}
}

func TestCreateValidation(t *testing.T) {
	s, _ := testService()
	ctx := context.Background()
	cases := []struct {
		name   string
		mutate func(*CreateInput)
	}{
		{"no name", func(in *CreateInput) { in.Name = " " }},
		{"no agents", func(in *CreateInput) { in.AgentIDs = nil }},
		{"no targets", func(in *CreateInput) { in.Targets = nil }},
		{"bad target", func(in *CreateInput) { in.Targets = []TargetInput{hostTarget("relative")} }},
		{"bad compression", func(in *CreateInput) { in.Compression = domain.Compression{Algorithm: "gzip"} }},
		{"encryption w/o key", func(in *CreateInput) { in.Encryption = domain.Encryption{Enabled: true} }},
		{"bad retention", func(in *CreateInput) { in.Retention = domain.RetentionRule{} }},
	}
	for _, tc := range cases {
		in := CreateInput{Name: "p", AgentIDs: []string{"a"}, Retention: testRetention(),
			Targets: []TargetInput{hostTarget("/x")}}
		tc.mutate(&in)
		if _, _, err := s.Create(ctx, in); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%s: want ErrValidation, got %v", tc.name, err)
		}
	}
}

func TestVersionImmutability(t *testing.T) {
	s, _ := testService()
	ctx := context.Background()
	p, _ := mustCreate(t, s, func(in *CreateInput) {
		in.Targets = []TargetInput{{
			Type: domain.TargetHostFilesystem, Selector: "/data",
			Include: []string{"*.db"}, Options: map[string]string{"symlink": "record"},
		}}
	})

	before, err := s.GetVersion(ctx, p.ID, 1)
	if err != nil {
		t.Fatalf("get v1: %v", err)
	}
	beforeBytes, _ := json.Marshal(before)

	// Edit (update config) creates v2.
	p2, v2, err := s.Update(ctx, p.ID, UpdateInput{
		Name: "daily-home-v2", AgentIDs: []string{"ag_1"},
		Retention: testRetention(),
		Targets:   []TargetInput{hostTarget("/data2")},
		UpdatedBy: "admin", Note: "retarget",
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if v2.Version != 2 || p2.CurrentVersion != 2 {
		t.Fatalf("edit must create v2, got %d/%d", v2.Version, p2.CurrentVersion)
	}

	// Old version bytes must be unchanged.
	after, err := s.GetVersion(ctx, p.ID, 1)
	if err != nil {
		t.Fatalf("get v1 after edit: %v", err)
	}
	afterBytes, _ := json.Marshal(after)
	if string(beforeBytes) != string(afterBytes) {
		t.Fatalf("v1 mutated by v2 publish:\nbefore %s\nafter  %s", beforeBytes, afterBytes)
	}

	// Mutating the returned version must not corrupt the store (fake copies).
	after.Targets[0].Selector = "/corrupted"
	again, _ := s.GetVersion(ctx, p.ID, 1)
	if again.Targets[0].Selector != "/data" {
		t.Fatalf("stored v1 corrupted via alias: %q", again.Targets[0].Selector)
	}

	// List versions: oldest first, both present.
	vs, err := s.ListVersions(ctx, p.ID)
	if err != nil || len(vs) != 2 || vs[0].Version != 1 || vs[1].Version != 2 {
		t.Fatalf("list versions: %+v, err=%v", vs, err)
	}
	if vs[1].Note != "retarget" {
		t.Fatalf("v2 note not preserved: %q", vs[1].Note)
	}
}

func TestPublishInputMutationIsolation(t *testing.T) {
	s, _ := testService()
	ctx := context.Background()
	p, _ := mustCreate(t, s, nil)
	inc := []string{"*.db"}
	opts := map[string]string{"symlink": "skip"}
	in := PublishInput{
		Targets: []TargetInput{{
			Type: domain.TargetHostFilesystem, Selector: "/iso",
			Include: inc, Options: opts,
		}},
		Retention: testRetention(),
		CreatedBy: "admin",
	}
	if _, err := s.PublishVersion(ctx, p.ID, in); err != nil {
		t.Fatalf("publish: %v", err)
	}
	// Mutate caller-owned slices/maps after the call.
	inc[0] = "*.corrupted"
	opts["symlink"] = "follow"
	in.Targets[0].Selector = "/corrupted"
	v, _ := s.GetVersion(ctx, p.ID, 2)
	if v.Targets[0].Selector != "/iso" || v.Targets[0].Include[0] != "*.db" || v.Targets[0].Options["symlink"] != "skip" {
		t.Fatalf("stored v2 aliases caller memory: %+v", v.Targets[0])
	}
}

func TestTargetOpsPublishVersions(t *testing.T) {
	s, _ := testService()
	ctx := context.Background()
	p, v1 := mustCreate(t, s, nil)
	firstID := v1.Targets[0].ID

	v2, err := s.AddTarget(ctx, p.ID, hostTarget("/extra"), ChangeMeta{Actor: "admin"})
	if err != nil {
		t.Fatalf("add target: %v", err)
	}
	if v2.Version != 2 || len(v2.Targets) != 2 {
		t.Fatalf("add must publish v2 with 2 targets: %+v", v2)
	}
	if v2.Targets[0].ID != firstID {
		t.Fatal("existing target IDs must survive across versions")
	}

	v3, err := s.UpdateTarget(ctx, p.ID, firstID, TargetInput{
		Type: domain.TargetHostFilesystem, Selector: "/moved",
		Exclude: []string{"*.tmp"},
	}, ChangeMeta{Actor: "admin"})
	if err != nil {
		t.Fatalf("update target: %v", err)
	}
	if v3.Version != 3 || v3.Targets[0].Selector != "/moved" || v3.Targets[0].ID != firstID {
		t.Fatalf("update must publish v3 preserving ID: %+v", v3.Targets[0])
	}

	if _, err := s.UpdateTarget(ctx, p.ID, "tgt_missing", hostTarget("/x"), ChangeMeta{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("update missing target: want ErrNotFound, got %v", err)
	}

	v4, err := s.RemoveTarget(ctx, p.ID, firstID, ChangeMeta{Actor: "admin"})
	if err != nil {
		t.Fatalf("remove target: %v", err)
	}
	if v4.Version != 4 || len(v4.Targets) != 1 {
		t.Fatalf("remove must publish v4 with 1 target: %+v", v4)
	}
	if _, err := s.RemoveTarget(ctx, p.ID, v4.Targets[0].ID, ChangeMeta{}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("remove last target: want ErrValidation, got %v", err)
	}
	if _, err := s.RemoveTarget(ctx, p.ID, "tgt_missing", ChangeMeta{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("remove missing target: want ErrNotFound, got %v", err)
	}
}

func TestEnableDisableNoNewVersion(t *testing.T) {
	s, _ := testService()
	ctx := context.Background()
	p, _ := mustCreate(t, s, nil)
	dis, err := s.SetEnabled(ctx, p.ID, false, "admin")
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if dis.Enabled || dis.CurrentVersion != 1 {
		t.Fatalf("disable must not publish: %+v", dis)
	}
	en, err := s.SetEnabled(ctx, p.ID, true, "admin")
	if err != nil || !en.Enabled || en.CurrentVersion != 1 {
		t.Fatalf("enable must not publish: %+v, err=%v", en, err)
	}
	if _, err := s.SetEnabled(ctx, "pol_missing", true, "a"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("enable missing: want ErrNotFound, got %v", err)
	}
}

func TestDeleteKeepsVersions(t *testing.T) {
	s, fp := testService()
	ctx := context.Background()
	p, _ := mustCreate(t, s, nil)
	if err := s.Delete(ctx, p.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.Get(ctx, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get after delete: want ErrNotFound, got %v", err)
	}
	// Versions survive so completed-job configs stay intact.
	if _, err := fp.GetVersion(ctx, p.ID, 1); err != nil {
		t.Fatalf("versions must survive policy delete: %v", err)
	}
	if err := s.Delete(ctx, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("double delete: want ErrNotFound, got %v", err)
	}
}

func TestReferenceChecks(t *testing.T) {
	fp := newFakePolicies()
	st := &fakeStorage{
		dests:   map[string]*domain.StorageDestination{"dst_1": {ID: "dst_1", Name: "minio", Bucket: "vaultix"}},
		secrets: map[string]*domain.SecretProfile{"sek_db": {ID: "sek_db"}, "sek_key": {ID: "sek_key"}},
	}
	fst := &fakeStacks{
		stacks:    map[string]*domain.ApplicationStack{"stk_1": {ID: "stk_1"}},
		resources: map[string][]domain.ApplicationStackResource{"stk_1": {{StackID: "stk_1", Selector: "/x"}}},
	}
	s := NewService(fp, WithStorage(st), WithStacks(fst))
	ctx := context.Background()

	base := func() CreateInput {
		return CreateInput{Name: "p", AgentIDs: []string{"a"}, Retention: testRetention(),
			Targets: []TargetInput{hostTarget("/x")}}
	}
	// Unknown destination.
	in := base()
	in.StorageID = "dst_missing"
	if _, _, err := s.Create(ctx, in); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("unknown destination: want ErrValidation, got %v", err)
	}
	// Known destination passes.
	in.StorageID = "dst_1"
	if _, _, err := s.Create(ctx, in); err != nil {
		t.Fatalf("known destination rejected: %v", err)
	}
	// Unknown credential profile.
	in2 := base()
	in2.Name = "p2"
	in2.Targets = []TargetInput{{Type: domain.TargetPostgres, Selector: "db", CredentialRef: "sek_missing"}}
	if _, _, err := s.Create(ctx, in2); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("unknown credential: want ErrValidation, got %v", err)
	}
	// Known credential passes.
	in2.Targets[0].CredentialRef = "sek_db"
	if _, _, err := s.Create(ctx, in2); err != nil {
		t.Fatalf("known credential rejected: %v", err)
	}
	// Unknown encryption key.
	in3 := base()
	in3.Name = "p3"
	in3.Encryption = domain.Encryption{Enabled: true, KeyRef: "sek_missing"}
	if _, _, err := s.Create(ctx, in3); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("unknown key ref: want ErrValidation, got %v", err)
	}
	// Unknown stack.
	in4 := base()
	in4.Name = "p4"
	in4.Targets = []TargetInput{{Type: domain.TargetAppStack, Selector: "stk_missing"}}
	if _, _, err := s.Create(ctx, in4); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("unknown stack: want ErrValidation, got %v", err)
	}
	// Stack without members.
	fst.stacks["stk_empty"] = &domain.ApplicationStack{ID: "stk_empty"}
	in4.Targets[0].Selector = "stk_empty"
	if _, _, err := s.Create(ctx, in4); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("memberless stack: want ErrValidation, got %v", err)
	}
	// Healthy stack passes.
	in4.Targets[0].Selector = "stk_1"
	if _, _, err := s.Create(ctx, in4); err != nil {
		t.Fatalf("healthy stack rejected: %v", err)
	}
}

func TestCurrentVersionAndList(t *testing.T) {
	s, _ := testService()
	ctx := context.Background()
	p, _ := mustCreate(t, s, nil)
	cur, err := s.CurrentVersion(ctx, p.ID)
	if err != nil || cur.Version != 1 {
		t.Fatalf("current version: %+v, err=%v", cur, err)
	}
	res, err := s.List(ctx, domain.ListOptions{})
	if err != nil || res.Total != 1 || !reflect.DeepEqual(res.Items[0].ID, p.ID) {
		t.Fatalf("list: %+v, err=%v", res, err)
	}
	if _, err := s.GetVersion(ctx, p.ID, 0); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("version 0: want ErrValidation, got %v", err)
	}
	if _, err := s.GetVersion(ctx, p.ID, 9); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing version: want ErrNotFound, got %v", err)
	}
}
