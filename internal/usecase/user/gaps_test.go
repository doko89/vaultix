package user

import (
	"context"
	"errors"
	"testing"

	"github.com/vaultix/vaultix/internal/domain"
)

var errBoom = errors.New("boom")

func TestGetUserPaths(t *testing.T) {
	svc, _, _, _ := newSvc()
	ctx := context.Background()
	u, err := svc.CreateUser(ctx, CreateInput{Username: "alice", Email: "a@example.com", Password: "password-123"}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetUser(ctx, u.ID)
	if err != nil || got.Username != "alice" || got.PasswordHash != "" {
		t.Fatalf("Get: %+v %v", got, err)
	}
	if _, err := svc.GetUser(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing err = %v", err)
	}
}

func TestUpdateUserPaths(t *testing.T) {
	svc, _, _, _ := newSvc()
	ctx := context.Background()
	u, err := svc.CreateUser(ctx, CreateInput{Username: "alice", Email: "a@example.com", Password: "password-123"}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateUser(ctx, "missing", UpdateInput{DisplayName: "x"}, "a"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing err = %v", err)
	}
	if _, err := svc.UpdateUser(ctx, u.ID, UpdateInput{Email: "not-an-email"}, "a"); err == nil {
		t.Fatal("bad email must fail")
	}
	got, err := svc.UpdateUser(ctx, u.ID, UpdateInput{Email: "new@example.com"}, "a")
	if err != nil || got.Email != "new@example.com" {
		t.Fatalf("email update: %+v %v", got, err)
	}
}

func TestRoleErrorPaths(t *testing.T) {
	svc, _, _, _ := newSvc()
	ctx := context.Background()
	u, err := svc.CreateUser(ctx, CreateInput{Username: "alice", Email: "a@example.com", Password: "password-123"}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListUserRoles(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("list roles missing err = %v", err)
	}
	if err := svc.GrantRole(ctx, "missing", domain.RoleOperator, "a"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("grant missing user err = %v", err)
	}
	if err := svc.GrantRole(ctx, u.ID, "nope", "a"); err == nil {
		t.Fatal("grant unknown role must fail")
	}
	if err := svc.RevokeRole(ctx, "missing", domain.RoleOperator, "a"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("revoke missing user err = %v", err)
	}
	if err := svc.RevokeRole(ctx, u.ID, "nope", "a"); err == nil {
		t.Fatal("revoke unknown role must fail")
	}
	// Revoking the admin role from a non-admin never trips break-glass.
	if err := svc.RevokeRole(ctx, u.ID, domain.RoleAdministrator, "a"); err != nil {
		t.Fatalf("revoke admin from non-admin: %v", err)
	}
}

func TestPasswordErrorPaths(t *testing.T) {
	svc, _, _, _ := newSvc()
	ctx := context.Background()
	if err := svc.SetPassword(ctx, "missing", "long-enough-1", "a"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("reset missing err = %v", err)
	}
	if err := svc.ChangePassword(ctx, "missing", "a", "long-enough-1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("change missing err = %v", err)
	}
	sso, err := svc.CreateUser(ctx, CreateInput{Username: "sso", Email: "sso@example.com"}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ChangePassword(ctx, sso.ID, "x", "long-enough-1"); err == nil {
		t.Fatal("OIDC-only user has no local password to change")
	}
}

func TestDisableMissingUser(t *testing.T) {
	svc, _, _, _ := newSvc()
	if err := svc.SetDisabled(context.Background(), "missing", true, "a"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestBreakGlassWithoutAdminRole(t *testing.T) {
	svc, repo, _, _ := newSvc()
	ctx := context.Background()
	op, err := svc.CreateUser(ctx, CreateInput{Username: "op", Email: "op@example.com", Password: "op-password-1"}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	delete(repo.roles, domain.RoleAdministrator)
	delete(repo.byRole, domain.RoleAdministrator)
	// No admin role provisioned: nothing to protect, disable proceeds.
	if err := svc.SetDisabled(ctx, op.ID, true, "a"); err != nil {
		t.Fatalf("disable: %v", err)
	}
}

func TestBreakGlassRepoErrors(t *testing.T) {
	svc, repo, _, _ := newSvc()
	ctx := context.Background()
	admin, err := svc.CreateUser(ctx, CreateInput{Username: "root", Email: "r@example.com", Password: "root-password-1", RoleNames: []string{domain.RoleAdministrator}}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	repo.failList = errBoom
	if err := svc.SetDisabled(ctx, admin.ID, true, "a"); !errors.Is(err, errBoom) {
		t.Fatalf("list err = %v", err)
	}
	repo.failList = nil
	repo.failRoles = errBoom
	if err := svc.SetDisabled(ctx, admin.ID, true, "a"); !errors.Is(err, errBoom) {
		t.Fatalf("roles err = %v", err)
	}
}

func TestNilOptionals(t *testing.T) {
	repo := newFakeRepo()
	svc, err := NewService(Deps{Repo: repo, Hash: fakeHash{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	u, err := svc.CreateUser(ctx, CreateInput{Username: "a", Email: "a@example.com", Password: "password-123"}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	// No sessions/audit wired: ops still succeed.
	if err := svc.SetPassword(ctx, u.ID, "another-pass-1", "a"); err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateUser(ctx, CreateInput{Username: "b", Email: "b@example.com", Password: "password-123"}, "seed")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetDisabled(ctx, second.ID, true, "a"); err != nil {
		t.Fatal(err)
	}
	if err := svc.GrantRole(ctx, u.ID, domain.RoleOperator, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListUsers(ctx, domain.ListOptions{}); err != nil {
		t.Fatal(err)
	}
	repo.failList = errBoom
	if _, err := svc.ListUsers(ctx, domain.ListOptions{}); !errors.Is(err, errBoom) {
		t.Fatalf("list err = %v", err)
	}
}
