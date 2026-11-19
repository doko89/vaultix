package repo

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vaultix/vaultix/db/gen"
	"github.com/vaultix/vaultix/internal/domain"
)

// UserRepo implements domain.UserRepository on users/roles/user_roles.
//
// Mapping notes: the schema has no username column, so Username is the
// email address (lookups are case-insensitive). LocalLoginDisabled is not
// persisted. Role permissions JSON is configuration owned elsewhere and is
// not mapped back onto domain.Role.
type UserRepo struct {
	baseRepo
}

// CreateUser inserts a local or OIDC-provisioned user. The ID is always
// database-generated; u.ID/CreatedAt/UpdatedAt are populated from the row.
// An empty PasswordHash stores NULL (OIDC-only user).
func (r *UserRepo) CreateUser(ctx context.Context, u *domain.User) error {
	if u == nil {
		return validationError("user is required")
	}
	if err := u.Validate(); err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.CreateUser(ctx, gen.CreateUserParams{
		Email:        u.Email,
		PasswordHash: textOrNull(u.PasswordHash),
		DisplayName:  u.DisplayName,
	})
	if err != nil {
		return mapErr(err)
	}
	// Apply the disabled flag when the caller created a disabled user;
	// CreateUser always inserts active rows.
	if u.Disabled {
		updated, err := r.q.SetUserActive(ctx, gen.SetUserActiveParams{ID: row.ID, IsActive: false})
		if err != nil {
			return mapErr(err)
		}
		row = updated
	}
	u.ID = uuidString(row.ID)
	u.Username = row.Email
	u.Disabled = !row.IsActive
	u.CreatedAt = timeOrZero(row.CreatedAt)
	u.UpdatedAt = timeOrZero(row.UpdatedAt)
	return nil
}

// GetUser returns one user by ID.
func (r *UserRepo) GetUser(ctx context.Context, id string) (*domain.User, error) {
	uid, err := mustParseUUID(id, "user id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetUserByID(ctx, gen.GetUserByIDParams{ID: uid})
	if err != nil {
		return nil, mapErr(err)
	}
	u := mapUser(row)
	return &u, nil
}

// GetUserByUsername resolves by email address (see mapping notes).
func (r *UserRepo) GetUserByUsername(ctx context.Context, username string) (*domain.User, error) {
	if username == "" {
		return nil, notFound("user")
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetUserByEmail(ctx, gen.GetUserByEmailParams{Lower: username})
	if err != nil {
		return nil, mapErr(err)
	}
	u := mapUser(row)
	return &u, nil
}

// UpdateUser rewrites the mutable user fields.
func (r *UserRepo) UpdateUser(ctx context.Context, u *domain.User) error {
	if u == nil {
		return validationError("user is required")
	}
	if err := u.Validate(); err != nil {
		return err
	}
	uid, err := mustParseUUID(u.ID, "user id")
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	_, err = r.q.UpdateUser(ctx, gen.UpdateUserParams{
		ID:           uid,
		Email:        u.Email,
		PasswordHash: textOrNull(u.PasswordHash),
		DisplayName:  u.DisplayName,
		IsActive:     !u.Disabled,
	})
	if err != nil {
		return mapErr(err)
	}
	return nil
}

// ListUsers returns one page of users filtered by email/display name.
func (r *UserRepo) ListUsers(ctx context.Context, opts domain.ListOptions) (domain.ListResult[domain.User], error) {
	limit, offset := page(opts)
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	rows, err := r.q.SearchUsers(ctx, gen.SearchUsersParams{
		Column1: opts.Query,
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		return domain.ListResult[domain.User]{}, mapErr(err)
	}
	total, err := r.q.CountSearchUsers(ctx, gen.CountSearchUsersParams{Column1: opts.Query})
	if err != nil {
		return domain.ListResult[domain.User]{}, mapErr(err)
	}
	out := make([]domain.User, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapUser(row))
	}
	return domain.ListResult[domain.User]{Items: out, Total: int(total)}, nil
}

// GetRole returns one role by ID.
func (r *UserRepo) GetRole(ctx context.Context, id string) (*domain.Role, error) {
	rid, err := mustParseUUID(id, "role id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetRoleByID(ctx, gen.GetRoleByIDParams{ID: rid})
	if err != nil {
		return nil, mapErr(err)
	}
	role := mapRole(row)
	return &role, nil
}

// GetRoleByName resolves a role by its unique name.
func (r *UserRepo) GetRoleByName(ctx context.Context, name string) (*domain.Role, error) {
	if name == "" {
		return nil, notFound("role")
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	row, err := r.q.GetRoleByName(ctx, gen.GetRoleByNameParams{Name: name})
	if err != nil {
		return nil, mapErr(err)
	}
	role := mapRole(row)
	return &role, nil
}

// ListRoles returns all roles ordered by name.
func (r *UserRepo) ListRoles(ctx context.Context) ([]domain.Role, error) {
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	rows, err := r.q.ListRoles(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]domain.Role, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapRole(row))
	}
	return out, nil
}

// ListUserRoles returns the roles granted to a user.
func (r *UserRepo) ListUserRoles(ctx context.Context, userID string) ([]domain.Role, error) {
	uid, err := mustParseUUID(userID, "user id")
	if err != nil {
		return nil, err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	rows, err := r.q.ListUserRoles(ctx, gen.ListUserRolesParams{UserID: uid})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]domain.Role, 0, len(rows))
	for _, row := range rows {
		out = append(out, mapRole(row))
	}
	return out, nil
}

// GrantRole grants a role; granting twice is a no-op. An empty grantedBy
// (JIT provisioning) stores NULL.
func (r *UserRepo) GrantRole(ctx context.Context, userID, roleID, grantedBy string) error {
	uid, err := mustParseUUID(userID, "user id")
	if err != nil {
		return err
	}
	rid, err := mustParseUUID(roleID, "role id")
	if err != nil {
		return err
	}
	by, err := mustOptUUID(grantedBy, "granted_by id")
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := r.q.GrantUserRole(ctx, gen.GrantUserRoleParams{UserID: uid, RoleID: rid, GrantedBy: by}); err != nil {
		return mapErr(err)
	}
	return nil
}

// RevokeRole removes one grant; revoking a missing grant is a no-op.
func (r *UserRepo) RevokeRole(ctx context.Context, userID, roleID string) error {
	uid, err := mustParseUUID(userID, "user id")
	if err != nil {
		return err
	}
	rid, err := mustParseUUID(roleID, "role id")
	if err != nil {
		return err
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	if err := r.q.RevokeUserRole(ctx, gen.RevokeUserRoleParams{UserID: uid, RoleID: rid}); err != nil {
		return mapErr(err)
	}
	return nil
}

// ReplaceUserRoles atomically sets the exact role set (used by OIDC claim
// refresh). All IDs are validated before the transaction opens.
func (r *UserRepo) ReplaceUserRoles(ctx context.Context, userID string, roleIDs []string) error {
	uid, err := mustParseUUID(userID, "user id")
	if err != nil {
		return err
	}
	rids := make([]pgtype.UUID, 0, len(roleIDs))
	for _, id := range roleIDs {
		rid, err := mustParseUUID(id, "role id")
		if err != nil {
			return err
		}
		rids = append(rids, rid)
	}
	ctx, cancel := withTimeout(ctx)
	defer cancel()
	return mapErr(r.inTx(ctx, func(q *gen.Queries) error {
		if err := q.RevokeAllUserRoles(ctx, gen.RevokeAllUserRolesParams{UserID: uid}); err != nil {
			return err
		}
		for _, rid := range rids {
			if err := q.GrantUserRole(ctx, gen.GrantUserRoleParams{
				UserID: uid, RoleID: rid, GrantedBy: pgtype.UUID{},
			}); err != nil {
				return err
			}
		}
		return nil
	}))
}

func mapUser(row gen.User) domain.User {
	return domain.User{
		ID:           uuidString(row.ID),
		Username:     row.Email,
		Email:        row.Email,
		DisplayName:  row.DisplayName,
		PasswordHash: textOrEmpty(row.PasswordHash),
		Disabled:     !row.IsActive,
		CreatedAt:    timeOrZero(row.CreatedAt),
		UpdatedAt:    timeOrZero(row.UpdatedAt),
	}
}

func mapRole(row gen.Role) domain.Role {
	return domain.Role{
		ID:          uuidString(row.ID),
		Name:        row.Name,
		Description: row.Description,
		CreatedAt:   timeOrZero(row.CreatedAt),
		UpdatedAt:   timeOrZero(row.UpdatedAt),
	}
}
