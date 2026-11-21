package agentupdate

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/vaultix/vaultix/internal/domain"
)

type fakeUpdateRepo struct {
	mu        sync.Mutex
	artifacts map[string]*domain.AgentArtifact
	slots     map[string]*domain.AgentUpdate
}

func newFakeUpdateRepo() *fakeUpdateRepo {
	return &fakeUpdateRepo{artifacts: map[string]*domain.AgentArtifact{}, slots: map[string]*domain.AgentUpdate{}}
}

func (f *fakeUpdateRepo) CreateArtifact(_ context.Context, a *domain.AgentArtifact) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.artifacts {
		if e.Version == a.Version && e.GOOS == a.GOOS && e.GOARCH == a.GOARCH {
			return fmt.Errorf("%w: artifact exists", domain.ErrConflict)
		}
	}
	if a.ID == "" {
		a.ID = fmt.Sprintf("art-%d", len(f.artifacts)+1)
	}
	cp := *a
	f.artifacts[a.ID] = &cp
	return nil
}

func (f *fakeUpdateRepo) ListArtifacts(_ context.Context) ([]domain.AgentArtifact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.AgentArtifact
	for _, a := range f.artifacts {
		cp := *a
		cp.Bytes = nil
		out = append(out, cp)
	}
	return out, nil
}

func (f *fakeUpdateRepo) ListPlatformArtifacts(_ context.Context, goos, goarch string) ([]domain.AgentArtifact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.AgentArtifact
	for _, a := range f.artifacts {
		if a.GOOS == goos && a.GOARCH == goarch {
			cp := *a
			cp.Bytes = nil
			out = append(out, cp)
		}
	}
	return out, nil
}

func (f *fakeUpdateRepo) GetArtifact(_ context.Context, id string) (*domain.AgentArtifact, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.artifacts[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *a
	return &cp, nil
}

func (f *fakeUpdateRepo) DeleteArtifact(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.artifacts[id]; !ok {
		return domain.ErrNotFound
	}
	delete(f.artifacts, id)
	return nil
}

func (f *fakeUpdateRepo) UpsertUpdate(_ context.Context, agentID, targetVersion, goos, goarch, requestedBy string) (*domain.AgentUpdate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u := &domain.AgentUpdate{
		AgentID: agentID, TargetVersion: targetVersion, GOOS: goos, GOARCH: goarch,
		Status: domain.AgentUpdatePending, RequestedBy: requestedBy,
	}
	f.slots[agentID] = u
	cp := *u
	return &cp, nil
}

func (f *fakeUpdateRepo) GetUpdate(_ context.Context, agentID string) (*domain.AgentUpdate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.slots[agentID]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *u
	return &cp, nil
}

func (f *fakeUpdateRepo) SetUpdateStatus(_ context.Context, agentID, status, errMsg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.slots[agentID]
	if !ok {
		return domain.ErrNotFound
	}
	u.Status = status
	u.Error = errMsg
	return nil
}

type fakeAgentRepo struct {
	mu     sync.Mutex
	agents map[string]*domain.Agent
}

func newFakeAgentRepo(seeds ...domain.Agent) *fakeAgentRepo {
	f := &fakeAgentRepo{agents: map[string]*domain.Agent{}}
	for _, a := range seeds {
		cp := a
		f.agents[a.ID] = &cp
	}
	return f
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

func testSetup() (*Service, *fakeUpdateRepo, *fakeAgentRepo) {
	updates := newFakeUpdateRepo()
	agents := newFakeAgentRepo(
		domain.Agent{ID: "ag-old", Hostname: "vm-old", OS: "linux", Architecture: "amd64", Version: "v1.0.0"},
		domain.Agent{ID: "ag-new", Hostname: "vm-new", OS: "linux", Architecture: "amd64", Version: "v2.0.0"},
		domain.Agent{ID: "ag-arm", Hostname: "vm-arm", OS: "linux", Architecture: "arm64", Version: "v1.0.0"},
		domain.Agent{ID: "ag-unknown", Hostname: "vm-legacy", Version: "v1.0.0"},
		domain.Agent{ID: "ag-revoked", Hostname: "vm-dead", OS: "linux", Architecture: "amd64", Version: "v1.0.0", Disabled: true},
	)
	svc, err := NewService(updates, agents)
	if err != nil {
		panic(err)
	}
	return svc, updates, agents
}

func mustUpload(t *testing.T, svc *Service, version, goos, goarch string) *domain.AgentArtifact {
	t.Helper()
	art, err := svc.UploadArtifact(context.Background(), version, goos, goarch, []byte("fake-binary-"+version+"-"+goarch), "admin")
	if err != nil {
		t.Fatalf("upload %s/%s/%s: %v", version, goos, goarch, err)
	}
	return art
}

func TestNewServiceRequiresRepos(t *testing.T) {
	if _, err := NewService(nil, newFakeAgentRepo()); err == nil {
		t.Fatal("want error for nil updates repo")
	}
	if _, err := NewService(newFakeUpdateRepo(), nil); err == nil {
		t.Fatal("want error for nil agents repo")
	}
}

func TestUploadValidation(t *testing.T) {
	svc, _, _ := testSetup()
	ctx := context.Background()
	if _, err := svc.UploadArtifact(ctx, "", "linux", "amd64", []byte("x"), ""); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("empty version: want ErrValidation, got %v", err)
	}
	if _, err := svc.UploadArtifact(ctx, "v2", "", "amd64", []byte("x"), ""); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("empty goos: want ErrValidation, got %v", err)
	}
	if _, err := svc.UploadArtifact(ctx, "v2", "linux", "amd64", nil, ""); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("empty bytes: want ErrValidation, got %v", err)
	}
	big := make([]byte, MaxArtifactBytes+1)
	if _, err := svc.UploadArtifact(ctx, "v2", "linux", "amd64", big, ""); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("oversize: want ErrValidation, got %v", err)
	}
	art := mustUpload(t, svc, "v2.0.0", "linux", "amd64")
	sum := sha256.Sum256([]byte("fake-binary-v2.0.0-amd64"))
	if string(art.SHA256) != string(sum[:]) {
		t.Fatal("sha256 not computed over bytes")
	}
	if art.Bytes != nil {
		t.Fatal("upload response must not echo bytes")
	}
	if _, err := svc.UploadArtifact(ctx, "v2.0.0", "linux", "amd64", []byte("other"), ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate triple: want ErrConflict, got %v", err)
	}
}

func TestRequestUpdateGuards(t *testing.T) {
	svc, _, _ := testSetup()
	ctx := context.Background()
	art := mustUpload(t, svc, "v2.0.0", "linux", "amd64")
	if _, err := svc.RequestUpdate(ctx, "missing", art.ID, ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing agent: want ErrNotFound, got %v", err)
	}
	if _, err := svc.RequestUpdate(ctx, "ag-old", "missing", ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing artifact: want ErrNotFound, got %v", err)
	}
	if _, err := svc.RequestUpdate(ctx, "ag-revoked", art.ID, ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("revoked agent: want ErrConflict, got %v", err)
	}
	if _, err := svc.RequestUpdate(ctx, "ag-new", art.ID, ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("not newer: want ErrConflict, got %v", err)
	}
	if _, err := svc.RequestUpdate(ctx, "ag-arm", art.ID, ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("platform mismatch: want ErrConflict, got %v", err)
	}
	// Unknown platform: admin picked explicitly, allowed.
	slot, err := svc.RequestUpdate(ctx, "ag-unknown", art.ID, "admin")
	if err != nil {
		t.Fatalf("unknown platform: %v", err)
	}
	if slot.TargetVersion != "v2.0.0" || slot.GOOS != "linux" || slot.GOARCH != "amd64" || slot.Status != domain.AgentUpdatePending {
		t.Fatalf("slot: %+v", slot)
	}
	slot, err = svc.RequestUpdate(ctx, "ag-old", art.ID, "admin")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if slot.Status != domain.AgentUpdatePending {
		t.Fatalf("slot status: %+v", slot)
	}
}

func TestPendingDirectiveFlow(t *testing.T) {
	svc, updates, agents := testSetup()
	ctx := context.Background()
	if dir, err := svc.PendingDirective(ctx, "ag-old"); err != nil || dir != nil {
		t.Fatalf("no slot: want (nil, nil), got (%v, %v)", dir, err)
	}
	art := mustUpload(t, svc, "v2.0.0", "linux", "amd64")
	if _, err := svc.RequestUpdate(ctx, "ag-old", art.ID, ""); err != nil {
		t.Fatalf("request: %v", err)
	}
	dir, err := svc.PendingDirective(ctx, "ag-old")
	if err != nil || dir == nil {
		t.Fatalf("directive: (%v, %v)", dir, err)
	}
	if dir.Version != "v2.0.0" || dir.SizeBytes != int64(len("fake-binary-v2.0.0-amd64")) {
		t.Fatalf("directive: %+v", dir)
	}
	// Out-of-band update settles the slot without a directive.
	ag, _ := agents.GetAgent(ctx, "ag-old")
	ag.Version = "v2.0.0"
	if err := agents.UpdateAgent(ctx, ag); err != nil {
		t.Fatalf("bump: %v", err)
	}
	if dir, err := svc.PendingDirective(ctx, "ag-old"); err != nil || dir != nil {
		t.Fatalf("settled: want (nil, nil), got (%v, %v)", dir, err)
	}
	slot, _ := updates.GetUpdate(ctx, "ag-old")
	if slot.Status != domain.AgentUpdateApplied {
		t.Fatalf("settled status: %+v", slot)
	}
}

func TestPendingDirectiveMissingArtifactFails(t *testing.T) {
	svc, updates, _ := testSetup()
	ctx := context.Background()
	art := mustUpload(t, svc, "v2.0.0", "linux", "amd64")
	if _, err := svc.RequestUpdate(ctx, "ag-old", art.ID, ""); err != nil {
		t.Fatalf("request: %v", err)
	}
	if err := svc.DeleteArtifact(ctx, art.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if dir, err := svc.PendingDirective(ctx, "ag-old"); err != nil || dir != nil {
		t.Fatalf("orphaned: want (nil, nil), got (%v, %v)", dir, err)
	}
	slot, _ := updates.GetUpdate(ctx, "ag-old")
	if slot.Status != domain.AgentUpdateFailed || slot.Error == "" {
		t.Fatalf("orphaned status: %+v", slot)
	}
}

func TestReportUpdate(t *testing.T) {
	svc, updates, _ := testSetup()
	ctx := context.Background()
	if err := svc.ReportUpdate(ctx, "ag-old", "bogus", "v2", ""); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("bad status: want ErrValidation, got %v", err)
	}
	// No slot: ignored, never an error.
	if err := svc.ReportUpdate(ctx, "ag-old", domain.AgentUpdateApplied, "v2.0.0", ""); err != nil {
		t.Fatalf("no slot report: %v", err)
	}
	art := mustUpload(t, svc, "v2.0.0", "linux", "amd64")
	if _, err := svc.RequestUpdate(ctx, "ag-old", art.ID, ""); err != nil {
		t.Fatalf("request: %v", err)
	}
	// Stale version: ignored, slot stays pending.
	if err := svc.ReportUpdate(ctx, "ag-old", domain.AgentUpdateApplied, "v1.5.0", ""); err != nil {
		t.Fatalf("stale report: %v", err)
	}
	slot, _ := updates.GetUpdate(ctx, "ag-old")
	if slot.Status != domain.AgentUpdatePending {
		t.Fatalf("stale clobbered slot: %+v", slot)
	}
	if err := svc.ReportUpdate(ctx, "ag-old", domain.AgentUpdateFailed, "v2.0.0", "swap failed"); err != nil {
		t.Fatalf("report: %v", err)
	}
	slot, _ = updates.GetUpdate(ctx, "ag-old")
	if slot.Status != domain.AgentUpdateFailed || slot.Error != "swap failed" {
		t.Fatalf("slot: %+v", slot)
	}
}

type fakeSource struct {
	rel   Release
	files map[string][]byte
	err   error
}

func (f *fakeSource) LatestRelease(_ context.Context) (Release, error) {
	if f.err != nil {
		return Release{}, f.err
	}
	return f.rel, nil
}

func (f *fakeSource) Download(_ context.Context, url string, _ int64) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	data, ok := f.files[url]
	if !ok {
		return nil, fmt.Errorf("no such file: %s", url)
	}
	return data, nil
}

func sumsFor(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var b strings.Builder
	for name, data := range files {
		sum := sha256.Sum256(data)
		fmt.Fprintf(&b, "%x  %s\n", sum, name)
	}
	return []byte(b.String())
}

func TestSyncReleases(t *testing.T) {
	ctx := context.Background()
	binAMD := []byte("agent-binary-amd64")
	binARM := []byte("agent-binary-arm64")
	newSvc := func(t *testing.T, src *fakeSource) (*Service, *fakeUpdateRepo) {
		t.Helper()
		updates := newFakeUpdateRepo()
		svc, err := NewService(updates, newFakeAgentRepo(), WithReleaseSource(src))
		if err != nil {
			t.Fatalf("new service: %v", err)
		}
		return svc, updates
	}

	t.Run("unconfigured", func(t *testing.T) {
		svc, _, _ := testSetup()
		if _, err := svc.SyncReleases(ctx); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("want ErrValidation, got %v", err)
		}
	})

	t.Run("caches missing binaries", func(t *testing.T) {
		src := &fakeSource{
			rel: Release{Tag: "v2.0.0", Assets: []ReleaseAsset{
				{Name: "vaultix-agent-linux-amd64", URL: "u-amd64"},
				{Name: "vaultix-agent-linux-arm64", URL: "u-arm64"},
				{Name: "sha256sums.txt", URL: "u-sums"},
			}},
			files: map[string][]byte{
				"u-amd64": binAMD,
				"u-arm64": binARM,
				"u-sums":  sumsFor(t, map[string][]byte{"vaultix-agent-linux-amd64": binAMD, "vaultix-agent-linux-arm64": binARM}),
			},
		}
		svc, updates := newSvc(t, src)
		status, err := svc.SyncReleases(ctx)
		if err != nil {
			t.Fatalf("sync: %v", err)
		}
		if len(status.Cached) != 2 {
			t.Fatalf("cached: %+v", status)
		}
		arts, _ := updates.ListArtifacts(ctx)
		if len(arts) != 2 {
			t.Fatalf("stored: %d", len(arts))
		}
		// Second sync is a no-op.
		status, err = svc.SyncReleases(ctx)
		if err != nil {
			t.Fatalf("resync: %v", err)
		}
		if len(status.Cached) != 0 {
			t.Fatalf("resync cached: %+v", status)
		}
	})

	t.Run("checksum mismatch", func(t *testing.T) {
		src := &fakeSource{
			rel: Release{Tag: "v9.9.9", Assets: []ReleaseAsset{
				{Name: "vaultix-agent-linux-amd64", URL: "u-amd64"},
				{Name: "sha256sums.txt", URL: "u-sums"},
			}},
			files: map[string][]byte{
				"u-amd64": binAMD,
				"u-sums":  sumsFor(t, map[string][]byte{"vaultix-agent-linux-amd64": []byte("other-bytes")}),
			},
		}
		svc, _ := newSvc(t, src)
		if _, err := svc.SyncReleases(ctx); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("want ErrValidation, got %v", err)
		}
	})

	t.Run("missing sums", func(t *testing.T) {
		src := &fakeSource{
			rel: Release{Tag: "v9.9.9", Assets: []ReleaseAsset{
				{Name: "vaultix-agent-linux-amd64", URL: "u-amd64"},
			}},
			files: map[string][]byte{"u-amd64": binAMD},
		}
		svc, _ := newSvc(t, src)
		if _, err := svc.SyncReleases(ctx); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("want ErrValidation, got %v", err)
		}
	})

	t.Run("source error", func(t *testing.T) {
		svc, _ := newSvc(t, &fakeSource{err: errors.New("boom")})
		if _, err := svc.SyncReleases(ctx); err == nil {
			t.Fatal("want error")
		}
	})
}

func TestBatchRequestUpdate(t *testing.T) {
	svc, _, _ := testSetup()
	ctx := context.Background()
	if _, err := svc.BatchRequestUpdate(ctx, nil, ""); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("empty: want ErrValidation, got %v", err)
	}
	big := make([]string, 201)
	for i := range big {
		big[i] = "ag-old"
	}
	if _, err := svc.BatchRequestUpdate(ctx, big, ""); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("oversize: want ErrValidation, got %v", err)
	}
	mustUpload(t, svc, "v2.0.0", "linux", "amd64")
	got, err := svc.BatchRequestUpdate(ctx, []string{"ag-old", "ag-new", "missing", "ag-old", "  "}, "")
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	byID := map[string]BatchResult{}
	for _, r := range got {
		byID[r.AgentID] = r
	}
	if len(got) != 3 {
		t.Fatalf("dedup/blank: %+v", got)
	}
	if byID["ag-old"].Status != BatchRequested || byID["ag-old"].TargetVersion != "v2.0.0" {
		t.Fatalf("old: %+v", byID["ag-old"])
	}
	if byID["ag-new"].Status != BatchSkipped {
		t.Fatalf("new: %+v", byID["ag-new"])
	}
	if byID["missing"].Status != BatchFailed || byID["missing"].Error == "" {
		t.Fatalf("missing: %+v", byID["missing"])
	}
}

func TestDescribe(t *testing.T) {
	svc, _, _ := testSetup()
	ctx := context.Background()
	info, err := svc.Describe(ctx, "ag-old")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if info.Available || info.Latest != nil || info.Pending != nil {
		t.Fatalf("empty: %+v", info)
	}
	mustUpload(t, svc, "v2.0.0", "linux", "amd64")
	mustUpload(t, svc, "v1.5.0", "linux", "arm64")
	info, err = svc.Describe(ctx, "ag-old")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if !info.Available || info.Latest == nil || info.Latest.Version != "v2.0.0" {
		t.Fatalf("available: %+v", info)
	}
	if info.Latest.Bytes != nil {
		t.Fatal("describe must not include bytes")
	}
	info, err = svc.Describe(ctx, "ag-arm")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if !info.Available || info.Latest.Version != "v1.5.0" {
		t.Fatalf("arm latest: %+v", info)
	}
	info, err = svc.Describe(ctx, "ag-new")
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if info.Available {
		t.Fatalf("up to date must not offer: %+v", info)
	}
}
