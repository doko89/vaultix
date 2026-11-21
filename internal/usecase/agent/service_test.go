package agent

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
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type fakeAgentRepo struct {
	mu         sync.Mutex
	agents     map[string]*domain.Agent
	creds      map[string]*domain.AgentCredential // by ID
	byHash     map[string]*domain.AgentCredential
	heartbeats map[string][]*domain.AgentHeartbeat
}

func newFakeAgentRepo() *fakeAgentRepo {
	return &fakeAgentRepo{
		agents:     map[string]*domain.Agent{},
		creds:      map[string]*domain.AgentCredential{},
		byHash:     map[string]*domain.AgentCredential{},
		heartbeats: map[string][]*domain.AgentHeartbeat{},
	}
}

func (f *fakeAgentRepo) CreateAgent(_ context.Context, a *domain.Agent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.agents[a.ID]; ok {
		return fmt.Errorf("%w: agent exists", domain.ErrConflict)
	}
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

func (f *fakeAgentRepo) ListAgents(_ context.Context, opts domain.ListOptions) (domain.ListResult[domain.Agent], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var all []domain.Agent
	for _, a := range f.agents {
		if opts.Query != "" && a.Hostname != opts.Query {
			continue
		}
		all = append(all, *a)
	}
	total := len(all)
	if opts.Offset < len(all) {
		all = all[opts.Offset:]
	} else {
		all = nil
	}
	if opts.Limit > 0 && len(all) > opts.Limit {
		all = all[:opts.Limit]
	}
	return domain.ListResult[domain.Agent]{Items: all, Total: total}, nil
}

func (f *fakeAgentRepo) StoreCredential(_ context.Context, c *domain.AgentCredential) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *c
	f.creds[c.ID] = &cp
	f.byHash[c.TokenHash] = &cp
	return nil
}

func (f *fakeAgentRepo) GetCredentialByHash(_ context.Context, hash string) (*domain.AgentCredential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.byHash[hash]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *c
	return &cp, nil
}

func (f *fakeAgentRepo) ListAgentCredentials(_ context.Context, agentID string) ([]domain.AgentCredential, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.AgentCredential
	for _, c := range f.creds {
		if c.AgentID == agentID {
			out = append(out, *c)
		}
	}
	return out, nil
}

func (f *fakeAgentRepo) UpdateCredential(_ context.Context, c *domain.AgentCredential) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.creds[c.ID]; !ok {
		return domain.ErrNotFound
	}
	cp := *c
	f.creds[c.ID] = &cp
	f.byHash[c.TokenHash] = &cp
	return nil
}

func (f *fakeAgentRepo) RecordHeartbeat(_ context.Context, h *domain.AgentHeartbeat) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *h
	f.heartbeats[h.AgentID] = append(f.heartbeats[h.AgentID], &cp)
	return nil
}

func (f *fakeAgentRepo) LatestHeartbeat(_ context.Context, agentID string) (*domain.AgentHeartbeat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	list := f.heartbeats[agentID]
	if len(list) == 0 {
		return nil, domain.ErrNotFound
	}
	cp := *list[len(list)-1]
	return &cp, nil
}

type fakePolicyRepo struct {
	mu       sync.Mutex
	policies []domain.BackupPolicy
}

func (f *fakePolicyRepo) CreatePolicy(_ context.Context, p *domain.BackupPolicy) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.policies = append(f.policies, *p)
	return nil
}

func (f *fakePolicyRepo) GetPolicy(_ context.Context, id string) (*domain.BackupPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.policies {
		if p.ID == id {
			cp := p
			return &cp, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (f *fakePolicyRepo) UpdatePolicy(_ context.Context, p *domain.BackupPolicy) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, q := range f.policies {
		if q.ID == p.ID {
			f.policies[i] = *p
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *fakePolicyRepo) DeletePolicy(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, p := range f.policies {
		if p.ID == id {
			f.policies = append(f.policies[:i], f.policies[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *fakePolicyRepo) ListPolicies(_ context.Context, opts domain.ListOptions) (domain.ListResult[domain.BackupPolicy], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	all := append([]domain.BackupPolicy(nil), f.policies...)
	total := len(all)
	if opts.Offset < len(all) {
		all = all[opts.Offset:]
	} else {
		all = nil
	}
	if opts.Limit > 0 && len(all) > opts.Limit {
		all = all[:opts.Limit]
	}
	return domain.ListResult[domain.BackupPolicy]{Items: all, Total: total}, nil
}

func (f *fakePolicyRepo) PublishVersion(_ context.Context, _ *domain.BackupPolicyVersion) error {
	return nil
}

func (f *fakePolicyRepo) GetVersion(_ context.Context, _ string, _ int) (*domain.BackupPolicyVersion, error) {
	return nil, domain.ErrNotFound
}

func (f *fakePolicyRepo) ListVersions(_ context.Context, _ string) ([]domain.BackupPolicyVersion, error) {
	return nil, nil
}

func testSetup() (*Service, *fakeAgentRepo, *fakePolicyRepo, *fakeClock) {
	agents := newFakeAgentRepo()
	policies := &fakePolicyRepo{}
	clock := &fakeClock{now: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)}
	var n int
	next := func(prefix string) func() (string, error) {
		return func() (string, error) {
			n++
			return fmt.Sprintf("%s-%d", prefix, n), nil
		}
	}
	svc, err := NewService(agents, policies, clock,
		WithMinVersion("1.2.0"),
		WithTokenGenerator(next("tok")),
		WithIDGenerator(next("id")),
	)
	if err != nil {
		panic(err)
	}
	return svc, agents, policies, clock
}

func TestNewServiceRequiresRepos(t *testing.T) {
	if _, err := NewService(nil, &fakePolicyRepo{}, nil); err == nil {
		t.Fatal("want error for nil AgentRepository")
	}
	if _, err := NewService(newFakeAgentRepo(), nil, nil); err == nil {
		t.Fatal("want error for nil PolicyRepository")
	}
	if _, err := NewService(newFakeAgentRepo(), &fakePolicyRepo{}, nil); err != nil {
		t.Fatalf("nil clock should default: %v", err)
	}
}

func TestTokenLifecycleIssueRedeem(t *testing.T) {
	svc, agents, _, clock := testSetup()
	ctx := context.Background()

	plaintext, cred, err := svc.IssueEnrollmentToken(ctx, time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if plaintext == "" {
		t.Fatal("want plaintext token")
	}
	if !cred.Enrollment || cred.AgentID != "" {
		t.Fatalf("want unclaimed enrollment credential, got %+v", cred)
	}
	if cred.TokenHash == plaintext {
		t.Fatal("plaintext stored at rest")
	}
	if cred.ExpiresAt == nil || !cred.ExpiresAt.Equal(clock.Now().Add(time.Hour)) {
		t.Fatalf("wrong expiry: %+v", cred.ExpiresAt)
	}

	ag, agentToken, err := svc.RedeemEnrollmentToken(ctx, plaintext, Registration{
		Hostname: "vm-01", OS: "linux", Architecture: "amd64", Version: "1.2.0",
	})
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	if ag.Hostname != "vm-01" || agentToken == "" || agentToken == plaintext {
		t.Fatalf("bad redeem result: %+v token=%q", ag, agentToken)
	}

	// Enrollment token is single-use: stored credential revoked.
	stored, err := agents.GetCredentialByHash(ctx, HashToken(plaintext))
	if err != nil {
		t.Fatalf("lookup enrollment cred: %v", err)
	}
	if !stored.Revoked {
		t.Fatal("enrollment credential not revoked after redeem")
	}

	// Bound credential authenticates.
	bound, err := svc.AuthenticateAgent(ctx, agentToken)
	if err != nil {
		t.Fatalf("authenticate bound credential: %v", err)
	}
	if bound.AgentID != ag.ID || bound.Enrollment {
		t.Fatalf("bad bound credential: %+v", bound)
	}

	// Enrollment token must not authenticate as an agent credential.
	if _, err := svc.AuthenticateAgent(ctx, plaintext); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("enrollment token auth: want ErrUnauthorized, got %v", err)
	}
}

func TestTokenReuseRejected(t *testing.T) {
	svc, _, _, _ := testSetup()
	ctx := context.Background()
	plaintext, _, err := svc.IssueEnrollmentToken(ctx, time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, _, err := svc.RedeemEnrollmentToken(ctx, plaintext, Registration{Hostname: "a"}); err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	if _, _, err := svc.RedeemEnrollmentToken(ctx, plaintext, Registration{Hostname: "b"}); !errors.Is(err, ErrTokenReused) {
		t.Fatalf("second redeem: want ErrTokenReused, got %v", err)
	}
}

func TestTokenExpiry(t *testing.T) {
	svc, _, _, clock := testSetup()
	ctx := context.Background()
	plaintext, _, err := svc.IssueEnrollmentToken(ctx, time.Minute)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	clock.advance(2 * time.Minute)
	if _, _, err := svc.RedeemEnrollmentToken(ctx, plaintext, Registration{Hostname: "a"}); !errors.Is(err, ErrExpiredToken) {
		t.Fatalf("redeem expired: want ErrExpiredToken, got %v", err)
	}
}

func TestTokenInvalid(t *testing.T) {
	svc, _, _, _ := testSetup()
	ctx := context.Background()
	if _, _, err := svc.RedeemEnrollmentToken(ctx, "nope", Registration{Hostname: "a"}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("unknown token: want ErrInvalidToken, got %v", err)
	}
	if _, _, err := svc.RedeemEnrollmentToken(ctx, "  ", Registration{Hostname: "a"}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("blank token: want ErrValidation, got %v", err)
	}
	if _, _, err := svc.RedeemEnrollmentToken(ctx, "tok-x", Registration{}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("blank hostname: want ErrValidation, got %v", err)
	}
	// Agent-bound credential is not an enrollment token.
	plaintext, _, _ := svc.IssueEnrollmentToken(ctx, time.Hour)
	_, agentToken, _ := svc.RedeemEnrollmentToken(ctx, plaintext, Registration{Hostname: "a"})
	if _, _, err := svc.RedeemEnrollmentToken(ctx, agentToken, Registration{Hostname: "b"}); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("redeem with agent credential: want ErrInvalidToken, got %v", err)
	}
}

func TestIssueTokenTTLBounds(t *testing.T) {
	svc, _, _, clock := testSetup()
	ctx := context.Background()
	if _, _, err := svc.IssueEnrollmentToken(ctx, MaxEnrollmentTTL+time.Second); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("oversize ttl: want ErrValidation, got %v", err)
	}
	_, cred, err := svc.IssueEnrollmentToken(ctx, 0)
	if err != nil {
		t.Fatalf("default ttl: %v", err)
	}
	if !cred.ExpiresAt.Equal(clock.Now().Add(DefaultEnrollmentTTL)) {
		t.Fatalf("want default ttl, got %v", cred.ExpiresAt)
	}
}

func enroll(t *testing.T, svc *Service) (*domain.Agent, string) {
	t.Helper()
	ctx := context.Background()
	plaintext, _, err := svc.IssueEnrollmentToken(ctx, time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	ag, token, err := svc.RedeemEnrollmentToken(ctx, plaintext, Registration{Hostname: "vm-01", Version: "1.2.0"})
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	return ag, token
}

func TestHeartbeatPresenceTransitions(t *testing.T) {
	svc, _, _, clock := testSetup()
	ctx := context.Background()
	ag, _ := enroll(t, svc)

	// Never reported -> offline.
	view, err := svc.GetAgent(ctx, ag.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if view.Status != domain.AgentStatusOffline {
		t.Fatalf("want offline, got %s", view.Status)
	}

	// Heartbeat -> online, facts refreshed.
	total, free := uint64(100), uint64(40)
	res, err := svc.Heartbeat(ctx, HeartbeatInput{
		AgentID: ag.ID, Version: "1.2.0", IPAddress: "10.0.0.2",
		DiskTotalBytes: &total, DiskFreeBytes: &free,
		Capabilities: []string{"docker"},
	})
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if res.Status != domain.AgentStatusOnline {
		t.Fatalf("want online, got %s", res.Status)
	}
	stored, _ := svc.GetAgent(ctx, ag.ID)
	if stored.Agent.IPAddress != "10.0.0.2" || !stored.Agent.HasCapability("docker") {
		t.Fatalf("presence facts not refreshed: %+v", stored.Agent)
	}
	if stored.Agent.DiskFreeBytes == nil || *stored.Agent.DiskFreeBytes != 40 {
		t.Fatalf("disk facts not refreshed: %+v", stored.Agent)
	}

	// Silence past the timeout -> stale.
	clock.advance(domain.DefaultHeartbeatTimeout + time.Minute)
	stale, _ := svc.GetAgent(ctx, ag.ID)
	if stale.Status != domain.AgentStatusStale {
		t.Fatalf("want stale, got %s", stale.Status)
	}

	// Revoked -> revoked regardless of heartbeat age.
	if err := svc.RevokeAgent(ctx, ag.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	revoked, _ := svc.GetAgent(ctx, ag.ID)
	if revoked.Status != domain.AgentStatusRevoked {
		t.Fatalf("want revoked, got %s", revoked.Status)
	}
	if _, err := svc.Heartbeat(ctx, HeartbeatInput{AgentID: ag.ID}); !errors.Is(err, ErrAgentRevoked) {
		t.Fatalf("heartbeat after revoke: want ErrAgentRevoked, got %v", err)
	}
}

func TestHeartbeatStoresSysinfo(t *testing.T) {
	svc, _, _, _ := testSetup()
	ctx := context.Background()
	ag, _ := enroll(t, svc)
	total, free := uint64(200), uint64(80)
	if _, err := svc.Heartbeat(ctx, HeartbeatInput{
		AgentID: ag.ID, Version: "1.2.0", OS: "linux", Architecture: "amd64",
		IPAddress: "10.0.0.3", DiskTotalBytes: &total, DiskFreeBytes: &free,
	}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	stored, _ := svc.GetAgent(ctx, ag.ID)
	if stored.Agent.OS != "linux" || stored.Agent.Architecture != "amd64" {
		t.Fatalf("os/arch not stored: %+v", stored.Agent)
	}
	if stored.Agent.DiskTotalBytes == nil || *stored.Agent.DiskTotalBytes != 200 {
		t.Fatalf("disk total not stored: %+v", stored.Agent)
	}
	// Omitted readings keep stored values instead of wiping them.
	if _, err := svc.Heartbeat(ctx, HeartbeatInput{AgentID: ag.ID, Version: "1.2.0"}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	kept, _ := svc.GetAgent(ctx, ag.ID)
	if kept.Agent.OS != "linux" || kept.Agent.Architecture != "amd64" {
		t.Fatalf("os/arch wiped by omission: %+v", kept.Agent)
	}
	if kept.Agent.DiskTotalBytes == nil || *kept.Agent.DiskTotalBytes != 200 {
		t.Fatalf("disk wiped by omission: %+v", kept.Agent)
	}
	if kept.Agent.DiskFreeBytes == nil || *kept.Agent.DiskFreeBytes != 80 {
		t.Fatalf("disk free wiped by omission: %+v", kept.Agent)
	}
}

func TestHeartbeatValidation(t *testing.T) {
	svc, _, _, _ := testSetup()
	ctx := context.Background()
	if _, err := svc.Heartbeat(ctx, HeartbeatInput{}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("blank agent: want ErrValidation, got %v", err)
	}
	ag, _ := enroll(t, svc)
	if _, err := svc.Heartbeat(ctx, HeartbeatInput{AgentID: ag.ID, RunningJobs: -1}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("negative jobs: want ErrValidation, got %v", err)
	}
	if _, err := svc.Heartbeat(ctx, HeartbeatInput{AgentID: "missing"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing agent: want ErrNotFound, got %v", err)
	}
}

func TestRevokeDeniesAuth(t *testing.T) {
	svc, _, _, _ := testSetup()
	ctx := context.Background()
	ag, token := enroll(t, svc)
	if err := svc.RevokeAgent(ctx, ag.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	// Credential revoked -> unauthorized (deny either way; agent is disabled).
	if _, err := svc.AuthenticateAgent(ctx, token); err == nil {
		t.Fatal("want auth denied after revoke")
	}
	// Disable is reversible: re-enable restores access with intact credentials.
	ag2, token2 := enroll(t, svc)
	if err := svc.SetDisabled(ctx, ag2.ID, true); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := svc.AuthenticateAgent(ctx, token2); !errors.Is(err, ErrAgentRevoked) {
		t.Fatalf("disabled auth: want ErrAgentRevoked, got %v", err)
	}
	if err := svc.SetDisabled(ctx, ag2.ID, false); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := svc.AuthenticateAgent(ctx, token2); err != nil {
		t.Fatalf("auth after re-enable: %v", err)
	}
}

func TestDeleteRequiresRevoked(t *testing.T) {
	svc, _, _, _ := testSetup()
	ctx := context.Background()
	if err := svc.DeleteAgent(ctx, ""); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("empty id: want ErrValidation, got %v", err)
	}
	if err := svc.DeleteAgent(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing: want ErrNotFound, got %v", err)
	}
	ag, _ := enroll(t, svc)
	if err := svc.DeleteAgent(ctx, ag.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("active agent: want ErrConflict, got %v", err)
	}
	if err := svc.RevokeAgent(ctx, ag.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := svc.DeleteAgent(ctx, ag.ID); err != nil {
		t.Fatalf("delete revoked: %v", err)
	}
	if _, err := svc.GetAgent(ctx, ag.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get after delete: want ErrNotFound, got %v", err)
	}
}

func TestAuthenticateFailures(t *testing.T) {
	svc, _, _, _ := testSetup()
	ctx := context.Background()
	if _, err := svc.AuthenticateAgent(ctx, ""); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("blank: want ErrUnauthorized, got %v", err)
	}
	if _, err := svc.AuthenticateAgent(ctx, "unknown"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("unknown: want ErrUnauthorized, got %v", err)
	}
	if err := svc.RevokeAgent(ctx, "  "); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("blank revoke id: want ErrValidation, got %v", err)
	}
	if err := svc.RevokeAgent(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing revoke: want ErrNotFound, got %v", err)
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"1.2.3", "1.2.4", -1},
		{"1.2.4", "1.2.3", 1},
		{"v1.10.0", "1.9.0", 1},
		{"1.2", "1.2.0", 0},
		{"1.2.3-rc1", "1.2.3", -1},
		{"1.2.3", "1.2.3-rc1", 1},
		{"1.2.3-rc1", "1.2.3-rc2", -1},
		{"", "1.0.0", -1},
		{"1.0.0", "", 1},
		{"", "", 0},
		{" 1.2.0 ", "1.2.0", 0},
	}
	for _, tc := range cases {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Fatalf("CompareVersions(%q,%q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestOutdatedFlag(t *testing.T) {
	svc, _, _, _ := testSetup()
	ctx := context.Background()
	if svc.IsOutdated("1.1.9") != true {
		t.Fatal("1.1.9 should be outdated vs 1.2.0")
	}
	if svc.IsOutdated("1.2.0") || svc.IsOutdated("v1.10.0") {
		t.Fatal("current/newer versions should not be outdated")
	}
	if !svc.IsOutdated("") {
		t.Fatal("empty version should be outdated when a minimum is set")
	}
	ag, _ := enroll(t, svc)
	if _, err := svc.Heartbeat(ctx, HeartbeatInput{AgentID: ag.ID, Version: "1.0.0"}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	view, _ := svc.GetAgent(ctx, ag.ID)
	if !view.Outdated {
		t.Fatal("agent on 1.0.0 should be flagged outdated")
	}

	plain, err := NewService(newFakeAgentRepo(), &fakePolicyRepo{}, nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if plain.IsOutdated("0.0.1") {
		t.Fatal("no minimum configured: nothing is outdated")
	}
}

type fakeUpdateRepo struct {
	artifacts []domain.AgentArtifact
	err       error
}

func (f *fakeUpdateRepo) CreateArtifact(_ context.Context, a *domain.AgentArtifact) error {
	f.artifacts = append(f.artifacts, *a)
	return nil
}

func (f *fakeUpdateRepo) ListArtifacts(_ context.Context) ([]domain.AgentArtifact, error) {
	if f.err != nil {
		return nil, f.err
	}
	return append([]domain.AgentArtifact(nil), f.artifacts...), nil
}

func (f *fakeUpdateRepo) ListPlatformArtifacts(_ context.Context, goos, goarch string) ([]domain.AgentArtifact, error) {
	var out []domain.AgentArtifact
	for _, a := range f.artifacts {
		if a.GOOS == goos && a.GOARCH == goarch {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *fakeUpdateRepo) GetArtifact(_ context.Context, id string) (*domain.AgentArtifact, error) {
	for _, a := range f.artifacts {
		if a.ID == id {
			cp := a
			return &cp, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (f *fakeUpdateRepo) DeleteArtifact(_ context.Context, id string) error {
	for i, a := range f.artifacts {
		if a.ID == id {
			f.artifacts = append(f.artifacts[:i], f.artifacts[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *fakeUpdateRepo) UpsertUpdate(_ context.Context, agentID, targetVersion, goos, goarch, requestedBy string) (*domain.AgentUpdate, error) {
	return &domain.AgentUpdate{AgentID: agentID, TargetVersion: targetVersion, GOOS: goos, GOARCH: goarch, RequestedBy: requestedBy}, nil
}

func (f *fakeUpdateRepo) GetUpdate(_ context.Context, _ string) (*domain.AgentUpdate, error) {
	return nil, domain.ErrNotFound
}

func (f *fakeUpdateRepo) SetUpdateStatus(_ context.Context, _, _, _ string) error {
	return nil
}

func TestOutdatedAgainstCache(t *testing.T) {
	ctx := context.Background()
	agents := newFakeAgentRepo()
	updates := &fakeUpdateRepo{artifacts: []domain.AgentArtifact{
		{ID: "a1", Version: "v2.0.0", GOOS: "linux", GOARCH: "amd64"},
		{ID: "a2", Version: "v1.5.0", GOOS: "linux", GOARCH: "arm64"},
	}}
	svc, err := NewService(agents, &fakePolicyRepo{}, nil, WithUpdateRepo(updates))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	old := &domain.Agent{ID: "ag-old", Hostname: "vm-old", OS: "linux", Architecture: "amd64", Version: "v1.0.0"}
	cur := &domain.Agent{ID: "ag-cur", Hostname: "vm-cur", OS: "linux", Architecture: "amd64", Version: "v2.0.0"}
	arm := &domain.Agent{ID: "ag-arm", Hostname: "vm-arm", OS: "linux", Architecture: "arm64", Version: "v1.0.0"}
	legacy := &domain.Agent{ID: "ag-legacy", Hostname: "vm-legacy", Version: "v1.0.0"}
	revoked := &domain.Agent{ID: "ag-rev", Hostname: "vm-rev", OS: "linux", Architecture: "amd64", Version: "v1.0.0", Disabled: true}
	for _, a := range []*domain.Agent{old, cur, arm, legacy, revoked} {
		if err := agents.CreateAgent(ctx, a); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	for id, want := range map[string]bool{"ag-old": true, "ag-cur": false, "ag-arm": true, "ag-legacy": true, "ag-rev": false} {
		view, err := svc.GetAgent(ctx, id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if view.Outdated != want {
			t.Fatalf("%s outdated=%v, want %v", id, view.Outdated, want)
		}
	}
	// Listings flag the same rows.
	listed, err := svc.ListAgents(ctx, domain.ListOptions{}, AgentFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, v := range listed.Items {
		if v.Agent.ID == "ag-old" && !v.Outdated {
			t.Fatal("list must flag ag-old")
		}
		if v.Agent.ID == "ag-cur" && v.Outdated {
			t.Fatal("list must not flag ag-cur")
		}
	}
	// Lookup failure falls back to the (empty) minimum: nothing flags.
	updates.err = errors.New("boom")
	view, err := svc.GetAgent(ctx, "ag-old")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if view.Outdated {
		t.Fatal("lookup failure must fall back, not flag")
	}
}

func TestListAgentsFilter(t *testing.T) {
	svc, _, _, clock := testSetup()
	ctx := context.Background()
	agOnline, _ := enroll(t, svc)
	if _, err := svc.Heartbeat(ctx, HeartbeatInput{AgentID: agOnline.ID, Version: "1.2.0"}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	agQuiet, _ := enroll(t, svc)
	_ = agQuiet
	clock.advance(domain.DefaultHeartbeatTimeout + time.Minute)
	// agOnline heartbeat is now stale; agQuiet never reported (offline).

	stale, err := svc.ListAgents(ctx, domain.ListOptions{Limit: 10}, AgentFilter{Status: domain.AgentStatusStale})
	if err != nil {
		t.Fatalf("list stale: %v", err)
	}
	if len(stale.Items) != 1 || stale.Items[0].Agent.ID != agOnline.ID {
		t.Fatalf("want only the stale agent, got %+v", stale.Items)
	}
	if stale.Total != 2 {
		t.Fatalf("want repo total 2, got %d", stale.Total)
	}
	offline, err := svc.ListAgents(ctx, domain.ListOptions{Limit: 10}, AgentFilter{Status: domain.AgentStatusOffline})
	if err != nil {
		t.Fatalf("list offline: %v", err)
	}
	if len(offline.Items) != 1 {
		t.Fatalf("want 1 offline agent, got %d", len(offline.Items))
	}
	if _, err := svc.ListAgents(ctx, domain.ListOptions{}, AgentFilter{Status: "bogus"}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("bad status: want ErrValidation, got %v", err)
	}
}

func TestAgentPolicies(t *testing.T) {
	svc, _, policies, _ := testSetup()
	ctx := context.Background()
	ag, _ := enroll(t, svc)
	policies.policies = []domain.BackupPolicy{
		{ID: "p1", Name: "daily", Enabled: true, CurrentVersion: 3, AgentIDs: []string{ag.ID}},
		{ID: "p2", Name: "other", Enabled: true, AgentIDs: []string{"someone-else"}},
		{ID: "p3", Name: "legacy", Enabled: false, CurrentVersion: 1, AgentIDs: []string{ag.ID, "x"}},
	}
	got, err := svc.AgentPolicies(ctx, ag.ID)
	if err != nil {
		t.Fatalf("policies: %v", err)
	}
	if len(got) != 2 || got[0].ID != "p1" || got[1].ID != "p3" {
		t.Fatalf("want p1+p3, got %+v", got)
	}
	if got[0].CurrentVersion != 3 || got[0].Name != "daily" {
		t.Fatalf("bad summary: %+v", got[0])
	}
	if _, err := svc.AgentPolicies(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing agent: want ErrNotFound, got %v", err)
	}
	if _, err := svc.AgentPolicies(ctx, ""); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("blank agent: want ErrValidation, got %v", err)
	}
}

func TestHashTokenStable(t *testing.T) {
	if HashToken("abc") != HashToken("abc") || HashToken("abc") == "abc" {
		t.Fatal("hash must be stable and opaque")
	}
	if HashToken("abc") == HashToken("abd") {
		t.Fatal("hash collision on distinct inputs")
	}
}
