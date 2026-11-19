package user

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/vaultix/vaultix/internal/domain"
)

type fakeRepo struct {
	mu      sync.Mutex
	users   map[string]*domain.User
	byName  map[string]string
	roles   map[string]*domain.Role
	byRole  map[string]string
	members map[string]map[string]bool
	seq     int
	// Fault injection for error-path tests.
	failList  error
	failRoles error
}

func newFakeRepo() *fakeRepo {
	f := &fakeRepo{
		users: map[string]*domain.User{}, byName: map[string]string{},
		roles: map[string]*domain.Role{}, byRole: map[string]string{},
		members: map[string]map[string]bool{},
	}
	for _, name := range []string{domain.RoleAdministrator, domain.RoleOperator, domain.RoleReadOnly} {
		f.roles[name] = &domain.Role{ID: "role-" + name, Name: name}
		f.byRole[name] = "role-" + name
	}
	return f
}

func (f *fakeRepo) CreateUser(_ context.Context, u *domain.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, dup := f.byName[u.Username]; dup {
		return domain.ErrConflict
	}
	f.seq++
	u.ID = "u1"
	if f.seq > 1 {
		u.ID = "u" + string(rune('0'+f.seq))
	}
	cp := *u
	f.users[u.ID] = &cp
	f.byName[u.Username] = u.ID
	return nil
}

func (f *fakeRepo) GetUser(_ context.Context, id string) (*domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *u
	return &cp, nil
}

func (f *fakeRepo) GetUserByUsername(_ context.Context, username string) (*domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.byName[username]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *f.users[id]
	return &cp, nil
}

func (f *fakeRepo) UpdateUser(_ context.Context, u *domain.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.users[u.ID]; !ok {
		return domain.ErrNotFound
	}
	cp := *u
	f.users[u.ID] = &cp
	return nil
}

func (f *fakeRepo) ListUsers(_ context.Context, _ domain.ListOptions) (domain.ListResult[domain.User], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failList != nil {
		return domain.ListResult[domain.User]{}, f.failList
	}
	var out []domain.User
	for _, u := range f.users {
		out = append(out, *u)
	}
	return domain.ListResult[domain.User]{Items: out, Total: len(out)}, nil
}

func (f *fakeRepo) GetRole(_ context.Context, id string) (*domain.Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.roles {
		if r.ID == id {
			cp := *r
			return &cp, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (f *fakeRepo) GetRoleByName(_ context.Context, name string) (*domain.Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.roles[name]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *r
	return &cp, nil
}

func (f *fakeRepo) ListRoles(_ context.Context) ([]domain.Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.Role
	for _, r := range f.roles {
		out = append(out, *r)
	}
	return out, nil
}

func (f *fakeRepo) ListUserRoles(_ context.Context, userID string) ([]domain.Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failRoles != nil {
		return nil, f.failRoles
	}
	var out []domain.Role
	for roleID := range f.members[userID] {
		for _, r := range f.roles {
			if r.ID == roleID {
				out = append(out, *r)
			}
		}
	}
	return out, nil
}

func (f *fakeRepo) GrantRole(_ context.Context, userID, roleID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.members[userID] == nil {
		f.members[userID] = map[string]bool{}
	}
	f.members[userID][roleID] = true
	return nil
}

func (f *fakeRepo) RevokeRole(_ context.Context, userID, roleID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.members[userID], roleID)
	return nil
}

func (f *fakeRepo) ReplaceUserRoles(_ context.Context, userID string, roleIDs []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.members[userID] = map[string]bool{}
	for _, id := range roleIDs {
		f.members[userID][id] = true
	}
	return nil
}

type fakeHash struct{}

func (fakeHash) Hash(p string) (string, error) {
	if p == "" {
		return "", errors.New("empty")
	}
	return "hash:" + p, nil
}

func (fakeHash) Verify(hash, password string) error {
	if hash != "hash:"+password {
		return errors.New("mismatch")
	}
	return nil
}

type fakeSessions struct {
	mu      sync.Mutex
	revoked []string
}

func (f *fakeSessions) DeleteUserSessions(_ context.Context, userID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, userID)
	return nil
}

type fakeAudit struct {
	mu     sync.Mutex
	events []AuditEvent
}

func (f *fakeAudit) RecordUserEvent(_ context.Context, e AuditEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
	return nil
}

func newSvc() (*Service, *fakeRepo, *fakeSessions, *fakeAudit) {
	repo, sess, audit := newFakeRepo(), &fakeSessions{}, &fakeAudit{}
	svc, err := NewService(Deps{Repo: repo, Hash: fakeHash{}, Sessions: sess, Audit: audit})
	if err != nil {
		panic(err)
	}
	return svc, repo, sess, audit
}

func TestNewServiceRequiresDeps(t *testing.T) {
	if _, err := NewService(Deps{}); err == nil {
		t.Fatal("empty deps must fail")
	}
}

func TestCreateUser(t *testing.T) {
	svc, repo, _, _ := newSvc()
	ctx := context.Background()
	u, err := svc.CreateUser(ctx, CreateInput{
		Username: "alice", Email: "alice@example.com", DisplayName: "Alice",
		Password: "long-enough-password", RoleNames: []string{domain.RoleOperator},
	}, "admin")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if u.PasswordHash != "" {
		t.Fatal("create response must not echo the hash")
	}
	stored, _ := repo.GetUser(ctx, u.ID)
	if !strings.HasPrefix(stored.PasswordHash, "hash:") {
		t.Fatalf("hash not stored: %q", stored.PasswordHash)
	}
	roles, _ := svc.ListUserRoles(ctx, u.ID)
	if len(roles) != 1 || roles[0].Name != domain.RoleOperator {
		t.Fatalf("roles: %+v", roles)
	}
	// Validation.
	if _, err := svc.CreateUser(ctx, CreateInput{Email: "x@example.com"}, "a"); err == nil {
		t.Fatal("missing username must fail")
	}
	if _, err := svc.CreateUser(ctx, CreateInput{Username: "b", Email: "bad"}, "a"); err == nil {
		t.Fatal("bad email must fail")
	}
	if _, err := svc.CreateUser(ctx, CreateInput{Username: "c", Email: "c@example.com", Password: "short"}, "a"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("short password err = %v", err)
	}
	// OIDC-only user: no password accepted.
	oidc, err := svc.CreateUser(ctx, CreateInput{Username: "sso", Email: "sso@example.com"}, "a")
	if err != nil {
		t.Fatalf("OIDC-only create: %v", err)
	}
	stored, _ = repo.GetUser(ctx, oidc.ID)
	if stored.PasswordHash != "" {
		t.Fatal("OIDC-only user must have no hash")
	}
}

func TestDisableBreakGlass(t *testing.T) {
	svc, _, sess, _ := newSvc()
	ctx := context.Background()
	admin, err := svc.CreateUser(ctx, CreateInput{Username: "root", Email: "root@example.com", Password: "root-password-1", RoleNames: []string{domain.RoleAdministrator}}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	// Last admin cannot be disabled.
	if err := svc.SetDisabled(ctx, admin.ID, true, "admin"); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("err = %v, want ErrLastAdmin", err)
	}
	// Second admin unlocks the first.
	admin2, err := svc.CreateUser(ctx, CreateInput{Username: "root2", Email: "root2@example.com", Password: "root-password-2", RoleNames: []string{domain.RoleAdministrator}}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetDisabled(ctx, admin.ID, true, admin2.ID); err != nil {
		t.Fatalf("disable with backup admin: %v", err)
	}
	if len(sess.revoked) != 1 || sess.revoked[0] != admin.ID {
		t.Fatalf("revoked = %v", sess.revoked)
	}
	// Non-admin disable works even when sole admin stands.
	op, err := svc.CreateUser(ctx, CreateInput{Username: "op", Email: "op@example.com", Password: "op-password-1", RoleNames: []string{domain.RoleOperator}}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetDisabled(ctx, op.ID, true, admin2.ID); err != nil {
		t.Fatal(err)
	}
	// Re-enable works.
	if err := svc.SetDisabled(ctx, op.ID, false, admin2.ID); err != nil {
		t.Fatal(err)
	}
}

func TestRevokeLastAdminRole(t *testing.T) {
	svc, _, _, _ := newSvc()
	ctx := context.Background()
	admin, err := svc.CreateUser(ctx, CreateInput{Username: "root", Email: "root@example.com", Password: "root-password-1", RoleNames: []string{domain.RoleAdministrator}}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RevokeRole(ctx, admin.ID, domain.RoleAdministrator, "x"); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("err = %v, want ErrLastAdmin", err)
	}
	// Granting then revoking a non-admin role is fine.
	if err := svc.GrantRole(ctx, admin.ID, domain.RoleOperator, "x"); err != nil {
		t.Fatal(err)
	}
	if err := svc.RevokeRole(ctx, admin.ID, domain.RoleOperator, "x"); err != nil {
		t.Fatal(err)
	}
}

func TestPasswords(t *testing.T) {
	svc, repo, sess, audit := newSvc()
	ctx := context.Background()
	u, err := svc.CreateUser(ctx, CreateInput{Username: "alice", Email: "a@example.com", Password: "old-password-1"}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ChangePassword(ctx, u.ID, "wrong", "new-password-1"); err == nil {
		t.Fatal("wrong old password must fail")
	}
	if err := svc.ChangePassword(ctx, u.ID, "old-password-1", "short"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("weak err = %v", err)
	}
	if err := svc.ChangePassword(ctx, u.ID, "old-password-1", "new-password-1"); err != nil {
		t.Fatalf("change: %v", err)
	}
	stored, _ := repo.GetUser(ctx, u.ID)
	if stored.PasswordHash != "hash:new-password-1" {
		t.Fatalf("hash = %q", stored.PasswordHash)
	}
	if len(sess.revoked) == 0 {
		t.Fatal("password change must revoke sessions")
	}
	if err := svc.SetPassword(ctx, u.ID, "reset-pass-12", "admin"); err != nil {
		t.Fatalf("reset: %v", err)
	}
	for _, e := range audit.events {
		blob := e.Action + e.Actor + e.Target + e.Result + e.Detail
		for _, secret := range []string{"old-password-1", "new-password-1", "reset-pass-12"} {
			if strings.Contains(blob, secret) {
				t.Fatalf("audit leaks password in %v", e)
			}
		}
	}
}

func TestUpdateAndListStripHashes(t *testing.T) {
	svc, _, _, _ := newSvc()
	ctx := context.Background()
	u, err := svc.CreateUser(ctx, CreateInput{Username: "alice", Email: "a@example.com", Password: "password-123"}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	dis := true
	updated, err := svc.UpdateUser(ctx, u.ID, UpdateInput{DisplayName: "A", LocalLoginDisabled: &dis}, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if updated.DisplayName != "A" || !updated.LocalLoginDisabled || updated.PasswordHash != "" {
		t.Fatalf("updated: %+v", updated)
	}
	listed, err := svc.ListUsers(ctx, domain.ListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range listed.Items {
		if it.PasswordHash != "" {
			t.Fatal("list must strip hashes")
		}
	}
	roles, err := svc.ListRoles(ctx)
	if err != nil || len(roles) != 3 {
		t.Fatalf("roles: %+v %v", roles, err)
	}
}
