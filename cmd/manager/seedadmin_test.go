package main

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/vaultix/vaultix/internal/domain"
	useruc "github.com/vaultix/vaultix/internal/usecase/user"
)

type seedUsers struct {
	mu     sync.Mutex
	users  map[string]*domain.User
	roles  map[string]*domain.Role // by name
	grants map[string][]string     // userID -> role names
	next   int
}

func newSeedUsers() *seedUsers {
	return &seedUsers{
		users: map[string]*domain.User{},
		roles: map[string]*domain.Role{
			domain.RoleAdministrator: {ID: "role-admin", Name: domain.RoleAdministrator},
			domain.RoleOperator:      {ID: "role-op", Name: domain.RoleOperator},
			domain.RoleReadOnly:      {ID: "role-ro", Name: domain.RoleReadOnly},
		},
		grants: map[string][]string{},
	}
}

func (f *seedUsers) CreateUser(_ context.Context, u *domain.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ex := range f.users {
		if ex.Username == u.Username {
			return domain.ErrConflict
		}
	}
	f.next++
	u.ID = "user-seed"
	f.users[u.ID] = u
	return nil
}
func (f *seedUsers) GetUser(_ context.Context, id string) (*domain.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	u, ok := f.users[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return u, nil
}
func (f *seedUsers) GetUserByUsername(_ context.Context, _ string) (*domain.User, error) {
	return nil, domain.ErrNotFound
}
func (f *seedUsers) UpdateUser(_ context.Context, u *domain.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[u.ID] = u
	return nil
}
func (f *seedUsers) ListUsers(_ context.Context, _ domain.ListOptions) (domain.ListResult[domain.User], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.User
	for _, u := range f.users {
		out = append(out, *u)
	}
	return domain.ListResult[domain.User]{Items: out, Total: len(out)}, nil
}
func (f *seedUsers) GetRole(_ context.Context, _ string) (*domain.Role, error) {
	return nil, domain.ErrNotFound
}
func (f *seedUsers) GetRoleByName(_ context.Context, name string) (*domain.Role, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.roles[name]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return r, nil
}
func (f *seedUsers) ListRoles(_ context.Context) ([]domain.Role, error) { return nil, nil }
func (f *seedUsers) ListUserRoles(_ context.Context, _ string) ([]domain.Role, error) {
	return nil, nil
}
func (f *seedUsers) GrantRole(_ context.Context, userID, roleID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for name, r := range f.roles {
		if r.ID == roleID {
			f.grants[userID] = append(f.grants[userID], name)
			return nil
		}
	}
	return domain.ErrNotFound
}
func (f *seedUsers) RevokeRole(_ context.Context, _, _ string) error { return nil }
func (f *seedUsers) ReplaceUserRoles(_ context.Context, _ string, _ []string) error {
	return nil
}

type seedHasher struct{}

func (seedHasher) Hash(password string) (string, error) {
	return fmt.Sprintf("fakehash-%d", len(password)), nil
}
func (seedHasher) Verify(_, _ string) error { return nil }

func seedService(t *testing.T, users *seedUsers) *useruc.Service {
	t.Helper()
	svc, err := useruc.NewService(useruc.Deps{Repo: users, Hash: seedHasher{}})
	require.NoError(t, err)
	return svc
}

func TestSeedAdminCreatesFirstAdmin(t *testing.T) {
	ctx := context.Background()
	users := newSeedUsers()
	u, err := runSeedAdmin(ctx, seedService(t, users), seedAdminOptions{
		username: "admin", password: "supersecret1", email: "admin@localhost",
	})
	require.NoError(t, err)
	require.Equal(t, "admin", u.Username)
	require.Empty(t, u.PasswordHash, "hashes are never echoed")
	stored, err := users.GetUser(ctx, u.ID)
	require.NoError(t, err)
	require.NotEmpty(t, stored.PasswordHash)
	require.NotEqual(t, "supersecret1", stored.PasswordHash)
	require.NotContains(t, stored.PasswordHash, "supersecret1")
	require.Contains(t, users.grants[u.ID], domain.RoleAdministrator)
}

func TestSeedAdminRefusesWhenUsersExist(t *testing.T) {
	ctx := context.Background()
	users := newSeedUsers()
	svc := seedService(t, users)
	_, err := runSeedAdmin(ctx, svc, seedAdminOptions{username: "admin", password: "supersecret1"})
	require.NoError(t, err)

	_, err = runSeedAdmin(ctx, svc, seedAdminOptions{username: "second", password: "supersecret2"})
	require.ErrorIs(t, err, errSeedAdminRefused)
}

func TestSeedAdminForceAddsAnother(t *testing.T) {
	ctx := context.Background()
	users := newSeedUsers()
	svc := seedService(t, users)
	_, err := runSeedAdmin(ctx, svc, seedAdminOptions{username: "admin", password: "supersecret1"})
	require.NoError(t, err)

	// Same-username force still conflicts at the repo (no silent overwrite).
	_, err = runSeedAdmin(ctx, svc, seedAdminOptions{username: "admin", password: "supersecret9", force: true})
	require.Error(t, err)

	u, err := runSeedAdmin(ctx, svc, seedAdminOptions{username: "second", password: "supersecret2", force: true})
	require.NoError(t, err)
	require.Contains(t, users.grants[u.ID], domain.RoleAdministrator)
}

func TestSeedAdminRequiresCredentials(t *testing.T) {
	ctx := context.Background()
	users := newSeedUsers()
	svc := seedService(t, users)
	_, err := runSeedAdmin(ctx, svc, seedAdminOptions{password: "supersecret1"})
	require.Error(t, err)
	_, err = runSeedAdmin(ctx, svc, seedAdminOptions{username: "admin"})
	require.Error(t, err)
}

func TestResolveSeedAdminOptions(t *testing.T) {
	t.Setenv(seedAdminUserEnv, "envadmin")
	t.Setenv(seedAdminPasswordEnv, "envsecret")
	t.Setenv(seedAdminEmailEnv, "env@example.com")

	// Flags win over env.
	got := resolveSeedAdminOptions("flagadmin", "flagsecret", "", false)
	require.Equal(t, "flagadmin", got.username)
	require.Equal(t, "flagsecret", got.password)
	require.Equal(t, "env@example.com", got.email)

	// Env fills blanks; email defaults to username@localhost.
	got = resolveSeedAdminOptions("", "", "", false)
	require.Equal(t, "envadmin", got.username)
	require.Equal(t, "envsecret", got.password)

	t.Setenv(seedAdminEmailEnv, "")
	got = resolveSeedAdminOptions("", "", "", false)
	require.Equal(t, "envadmin@localhost", got.email)
}
