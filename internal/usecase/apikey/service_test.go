package apikey

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
	authsvc "github.com/vaultix/vaultix/internal/usecase/auth"
)

type fakeKeys struct {
	mu     sync.Mutex
	seq    int
	byID   map[string]*domain.ApiKey
	byHash map[string]string // string(hash) -> id
	touch  map[string]time.Time
}

func newFakeKeys() *fakeKeys {
	return &fakeKeys{byID: map[string]*domain.ApiKey{}, byHash: map[string]string{}, touch: map[string]time.Time{}}
}

func (f *fakeKeys) Create(_ context.Context, k *domain.ApiKey) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if h := string(k.KeyHash); h != "" {
		if _, dup := f.byHash[h]; dup {
			return fmt.Errorf("dup: %w", domain.ErrConflict)
		}
	}
	f.seq++
	k.ID = fmt.Sprintf("key-%d", f.seq)
	cp := *k
	f.byID[k.ID] = &cp
	f.byHash[string(k.KeyHash)] = k.ID
	return nil
}

func (f *fakeKeys) GetByID(_ context.Context, id string) (*domain.ApiKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k, ok := f.byID[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *k
	return &cp, nil
}

func (f *fakeKeys) GetByHash(_ context.Context, hash []byte) (*domain.ApiKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.byHash[string(hash)]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *f.byID[id]
	return &cp, nil
}

func (f *fakeKeys) ListByUser(_ context.Context, userID string) ([]domain.ApiKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.ApiKey
	for _, k := range f.byID {
		if k.UserID == userID {
			out = append(out, *k)
		}
	}
	return out, nil
}

func (f *fakeKeys) Revoke(_ context.Context, id string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k, ok := f.byID[id]
	if !ok {
		return domain.ErrNotFound
	}
	if k.RevokedAt == nil {
		k.RevokedAt = &at
	}
	return nil
}

func (f *fakeKeys) TouchLastUsed(_ context.Context, id string, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k, ok := f.byID[id]
	if !ok {
		return domain.ErrNotFound
	}
	k.LastUsedAt = &at
	f.touch[id] = at
	return nil
}

type fakeUsers struct {
	users map[string]*domain.User
	roles map[string][]domain.Role
}

func (f *fakeUsers) CreateUser(_ context.Context, u *domain.User) error {
	f.users[u.ID] = u
	return nil
}

func (f *fakeUsers) GetUser(_ context.Context, id string) (*domain.User, error) {
	u, ok := f.users[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *u
	return &cp, nil
}

func (f *fakeUsers) GetUserByUsername(_ context.Context, _ string) (*domain.User, error) {
	return nil, domain.ErrNotFound
}

func (f *fakeUsers) UpdateUser(_ context.Context, u *domain.User) error {
	f.users[u.ID] = u
	return nil
}

func (f *fakeUsers) ListUsers(_ context.Context, _ domain.ListOptions) (domain.ListResult[domain.User], error) {
	return domain.ListResult[domain.User]{}, nil
}

func (f *fakeUsers) GetRole(_ context.Context, _ string) (*domain.Role, error) {
	return nil, domain.ErrNotFound
}

func (f *fakeUsers) GetRoleByName(_ context.Context, _ string) (*domain.Role, error) {
	return nil, domain.ErrNotFound
}

func (f *fakeUsers) ListRoles(_ context.Context) ([]domain.Role, error) { return nil, nil }

func (f *fakeUsers) ListUserRoles(_ context.Context, userID string) ([]domain.Role, error) {
	return f.roles[userID], nil
}

func (f *fakeUsers) GrantRole(_ context.Context, _, _, _ string) error { return nil }

func (f *fakeUsers) RevokeRole(_ context.Context, _, _ string) error { return nil }

func (f *fakeUsers) ReplaceUserRoles(_ context.Context, _ string, _ []string) error { return nil }

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type fakeAudit struct{ events []authsvc.AuditEvent }

func (a *fakeAudit) RecordAuthEvent(_ context.Context, e authsvc.AuditEvent) error {
	a.events = append(a.events, e)
	return nil
}

func newSvc() (*Service, *fakeKeys, *fakeUsers, *fakeAudit, *fakeClock) {
	keys := newFakeKeys()
	users := &fakeUsers{
		users: map[string]*domain.User{
			"u-admin": {ID: "u-admin", Username: "root", Email: "root@example.com"},
			"u-alice": {ID: "u-alice", Username: "alice", Email: "alice@example.com"},
			"u-bob":   {ID: "u-bob", Username: "bob", Email: "bob@example.com"},
			"u-off":   {ID: "u-off", Username: "off", Email: "off@example.com", Disabled: true},
		},
		roles: map[string][]domain.Role{
			"u-admin": {{ID: "r1", Name: domain.RoleAdministrator}},
			"u-alice": {{ID: "r2", Name: domain.RoleOperator}},
		},
	}
	audit := &fakeAudit{}
	clock := &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	svc, err := NewService(Deps{Keys: keys, Users: users, Audit: audit, Clock: clock})
	if err != nil {
		panic(err)
	}
	return svc, keys, users, audit, clock
}

func TestNewServiceRequiresDeps(t *testing.T) {
	if _, err := NewService(Deps{}); err == nil {
		t.Fatal("nil deps must fail")
	}
}

func TestIssueVerifyRoundTrip(t *testing.T) {
	svc, _, _, audit, clock := newSvc()
	ctx := context.Background()
	issued, err := svc.Issue(ctx, "u-alice", "cli", []string{"read", "write"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !ValidSecretShape(issued.Secret) {
		t.Fatalf("bad secret shape %q", issued.Secret)
	}
	if issued.Key.KeyHash != nil {
		t.Fatal("issued metadata must not carry the hash")
	}
	if issued.Key.KeyPrefix != DisplayPrefix(issued.Secret) {
		t.Fatal("prefix mismatch")
	}
	if issued.Key.ExpiresAt == nil || !issued.Key.ExpiresAt.Equal(clock.now.Add(time.Hour)) {
		t.Fatalf("expiry not set: %+v", issued.Key.ExpiresAt)
	}
	got, err := svc.Verify(ctx, issued.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if got.Key.ID != issued.Key.ID || got.User.ID != "u-alice" {
		t.Fatalf("verify mismatch: %+v", got)
	}
	if got.Key.KeyHash != nil {
		t.Fatal("verified metadata must not carry the hash")
	}
	if got.User.PasswordHash != "" {
		t.Fatal("verified user must not carry a password hash")
	}
	if got.Key.LastUsedAt == nil || !got.Key.LastUsedAt.Equal(clock.now) {
		t.Fatal("last_used must be touched on verify")
	}
	if len(audit.events) == 0 || audit.events[0].Action != "apikey.issue" {
		t.Fatalf("issue must audit, got %+v", audit.events)
	}
}

func TestIssueValidationMatrix(t *testing.T) {
	svc, _, _, _, _ := newSvc()
	ctx := context.Background()
	cases := []struct {
		name   string
		user   string
		key    string
		scopes []string
		ttl    time.Duration
		want   error
	}{
		{"empty name", "u-alice", "  ", []string{"read"}, time.Hour, domain.ErrValidation},
		{"no scopes", "u-alice", "k", nil, time.Hour, domain.ErrValidation},
		{"bad scope", "u-alice", "k", []string{"root"}, time.Hour, domain.ErrValidation},
		{"negative ttl", "u-alice", "k", []string{"read"}, -time.Hour, domain.ErrValidation},
		{"tiny ttl", "u-alice", "k", []string{"read"}, time.Second, domain.ErrValidation},
		{"huge ttl", "u-alice", "k", []string{"read"}, 366 * 24 * time.Hour, domain.ErrValidation},
		{"missing user", "nope", "k", []string{"read"}, time.Hour, domain.ErrNotFound},
		{"disabled user", "u-off", "k", []string{"read"}, time.Hour, domain.ErrForbidden},
		{"admin scope non-admin", "u-alice", "k", []string{"admin"}, time.Hour, domain.ErrForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Issue(ctx, tc.user, tc.key, tc.scopes, tc.ttl)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	if _, err := svc.Issue(ctx, "u-admin", "adm", []string{"admin"}, 0); err != nil {
		t.Fatalf("admin may issue admin scope: %v", err)
	}
	if _, err := svc.Issue(ctx, "u-alice", "noexp", []string{" READ "}, 0); err != nil {
		t.Fatalf("ttl 0 + scope normalization must pass: %v", err)
	}
}

func TestVerifyMatrix(t *testing.T) {
	svc, keys, _, audit, clock := newSvc()
	ctx := context.Background()
	good, err := svc.Issue(ctx, "u-alice", "good", []string{"read"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	expired, err := svc.Issue(ctx, "u-alice", "exp", []string{"read"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// Force expiry in the store.
	keys.mu.Lock()
	past := clock.now.Add(-time.Minute)
	keys.byID[expired.Key.ID].ExpiresAt = &past
	keys.mu.Unlock()
	revoked, err := svc.Issue(ctx, "u-alice", "rev", []string{"read"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Revoke(ctx, "u-alice", revoked.Key.ID); err != nil {
		t.Fatal(err)
	}

	other, _ := GenerateSecret()
	cases := []struct {
		name   string
		secret string
		want   error
	}{
		{"valid", good.Secret, nil},
		{"wrong key", other, ErrInvalidKey},
		{"garbage", "not-a-key", ErrInvalidKey},
		{"empty", "", ErrInvalidKey},
		{"wrong scheme", "zz_live_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", ErrInvalidKey},
		{"truncated", good.Secret[:len(good.Secret)-1], ErrInvalidKey},
		{"expired", expired.Secret, ErrKeyExpired},
		{"revoked", revoked.Secret, ErrKeyRevoked},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Verify(ctx, tc.secret)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("verify: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
	// Every failure above except "valid" must have audited a failed verify.
	fails := 0
	for _, e := range audit.events {
		if e.Action == "apikey.verify" && (e.Result == "failed" || e.Result == "denied") {
			fails++
			if e.Detail == "" {
				t.Fatal("failed verify must carry a detail class")
			}
		}
	}
	if fails != len(cases)-1 {
		t.Fatalf("failed-verify audits = %d, want %d", fails, len(cases)-1)
	}
}

func TestVerifyDisabledOwnerRejected(t *testing.T) {
	svc, _, users, _, _ := newSvc()
	ctx := context.Background()
	issued, err := svc.Issue(ctx, "u-alice", "k", []string{"read"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := *users.users["u-alice"]
	u.Disabled = true
	users.users["u-alice"] = &u
	if _, err := svc.Verify(ctx, issued.Secret); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("disabled owner err = %v", err)
	}
}

func TestListStripsSecrets(t *testing.T) {
	svc, _, _, _, _ := newSvc()
	ctx := context.Background()
	issued, err := svc.Issue(ctx, "u-alice", "cli", []string{"read"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.List(ctx, "u-alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != issued.Key.ID {
		t.Fatalf("list = %+v", got)
	}
	if got[0].KeyHash != nil {
		t.Fatal("list must never return hashes")
	}
	if other, err := svc.List(ctx, "u-bob"); err != nil || len(other) != 0 {
		t.Fatalf("bob must see no keys: %+v %v", other, err)
	}
}

func TestRevokeMatrix(t *testing.T) {
	svc, _, _, _, _ := newSvc()
	ctx := context.Background()
	alice, _ := svc.Issue(ctx, "u-alice", "a", []string{"read"}, 0)
	bob, _ := svc.Issue(ctx, "u-bob", "b", []string{"read"}, 0)

	if err := svc.Revoke(ctx, "u-bob", alice.Key.ID); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("non-owner revoke err = %v", err)
	}
	if err := svc.Revoke(ctx, "u-admin", bob.Key.ID); err != nil {
		t.Fatalf("admin revoke: %v", err)
	}
	if err := svc.Revoke(ctx, "u-alice", alice.Key.ID); err != nil {
		t.Fatalf("owner revoke: %v", err)
	}
	if err := svc.Revoke(ctx, "u-alice", alice.Key.ID); err != nil {
		t.Fatalf("idempotent re-revoke: %v", err)
	}
	if err := svc.Revoke(ctx, "u-alice", "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing revoke err = %v", err)
	}
	if _, err := svc.Verify(ctx, alice.Secret); !errors.Is(err, ErrKeyRevoked) {
		t.Fatalf("revoked key must not verify: %v", err)
	}
}

func TestSecretShape(t *testing.T) {
	for i := 0; i < 50; i++ {
		s, err := GenerateSecret()
		if err != nil {
			t.Fatal(err)
		}
		if !ValidSecretShape(s) {
			t.Fatalf("generated shape invalid: %q", s)
		}
		if len(s) != len(KeyScheme)+32 {
			t.Fatalf("length = %d", len(s))
		}
	}
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		s, _ := GenerateSecret()
		if seen[s] {
			t.Fatal("duplicate secret")
		}
		seen[s] = true
	}
	for _, bad := range []string{"", "vxk_", "vxk_short", "VXK_" + string(make([]byte, 32))} {
		if ValidSecretShape(bad) {
			t.Fatalf("shape %q must be invalid", bad)
		}
	}
}

func TestDisplayPrefix(t *testing.T) {
	s, _ := GenerateSecret()
	if got := DisplayPrefix(s); len(got) != 8 || got != s[:8] {
		t.Fatalf("prefix = %q", got)
	}
}
