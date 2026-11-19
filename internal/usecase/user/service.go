// Break-glass guards keep at least one active local administrator at all times.
package user

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vaultix/vaultix/internal/domain"
)

var (
	// ErrLastAdmin is returned when an operation would remove the final
	// active local administrator (PRD §7.1 break-glass).
	ErrLastAdmin = errors.New("user: cannot remove the last active administrator")
	// ErrWeakPassword is returned for empty admin-set passwords.
	ErrWeakPassword = errors.New("user: password does not meet policy")
)

// Hasher hashes passwords (wired to auth.BcryptHasher in cmd; declared here
// so this package does not import a sibling usecase).
type Hasher interface {
	Hash(password string) (string, error)
	Verify(hash, password string) error
}

// SessionRevoker ends a user's sessions (domain.SessionStore satisfies it).
type SessionRevoker interface {
	DeleteUserSessions(ctx context.Context, userID, exceptID string) error
}

// AuditEvent records user-administration actions. It never carries secrets.
type AuditEvent struct {
	At     time.Time
	Action string
	Actor  string
	Target string
	Result string
	Detail string
}

// Auditor records security events (platform track: audit log impl).
type Auditor interface {
	RecordUserEvent(ctx context.Context, e AuditEvent) error
}

// Deps wires the Service. Repo and Hash are required; Sessions and Audit
// are optional (nil disables revocation/auditing — tests only).
type Deps struct {
	Repo              domain.UserRepository
	Hash              Hasher
	Sessions          SessionRevoker
	Audit             Auditor
	Clock             domain.Clock
	MinPasswordLength int
}

// Service implements user administration.
type Service struct {
	repo     domain.UserRepository
	hash     Hasher
	sessions SessionRevoker
	audit    Auditor
	clock    domain.Clock
	minPass  int
}

// NewService wires a Service.
func NewService(d Deps) (*Service, error) {
	if d.Repo == nil || d.Hash == nil {
		return nil, errors.New("user: missing required dependencies")
	}
	if d.Clock == nil {
		d.Clock = domain.SystemClock{}
	}
	if d.MinPasswordLength <= 0 {
		d.MinPasswordLength = 8
	}
	return &Service{repo: d.Repo, hash: d.Hash, sessions: d.Sessions, audit: d.Audit, clock: d.Clock, minPass: d.MinPasswordLength}, nil
}

// CreateInput carries user creation (password optional for OIDC-only users).
type CreateInput struct {
	Username    string
	Email       string
	DisplayName string
	Password    string // empty => OIDC-only (no local password)
	RoleNames   []string
}

// CreateUser creates a local user, hashes its password, and grants roles.
func (s *Service) CreateUser(ctx context.Context, in CreateInput, actorID string) (*domain.User, error) {
	u := &domain.User{
		Username:    strings.TrimSpace(in.Username),
		Email:       strings.TrimSpace(in.Email),
		DisplayName: in.DisplayName,
	}
	if err := u.Validate(); err != nil {
		return nil, err
	}
	if in.Password != "" {
		if len(in.Password) < s.minPass {
			return nil, fmt.Errorf("%w: minimum length %d", ErrWeakPassword, s.minPass)
		}
		hash, err := s.hash.Hash(in.Password)
		if err != nil {
			return nil, fmt.Errorf("user: password hash: %w", err)
		}
		u.PasswordHash = hash
	}
	if err := s.repo.CreateUser(ctx, u); err != nil {
		return nil, fmt.Errorf("user: create: %w", err)
	}
	for _, name := range in.RoleNames {
		role, err := s.repo.GetRoleByName(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("user: role %q: %w", name, err)
		}
		if err := s.repo.GrantRole(ctx, u.ID, role.ID, actorID); err != nil {
			return nil, fmt.Errorf("user: grant role: %w", err)
		}
	}
	_ = s.record(ctx, AuditEvent{Action: "user.create", Actor: actorID, Target: u.ID, Result: "success", Detail: "user:" + u.Username})
	out := *u
	out.PasswordHash = "" // never echo hashes
	return &out, nil
}

// GetUser returns a user without the password hash.
func (s *Service) GetUser(ctx context.Context, id string) (*domain.User, error) {
	u, err := s.repo.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	out := *u
	out.PasswordHash = ""
	return &out, nil
}

// ListUsers pages users with hashes stripped.
func (s *Service) ListUsers(ctx context.Context, opts domain.ListOptions) (domain.ListResult[domain.User], error) {
	res, err := s.repo.ListUsers(ctx, opts)
	if err != nil {
		return res, err
	}
	for i := range res.Items {
		res.Items[i].PasswordHash = ""
	}
	return res, nil
}

// UpdateInput carries mutable profile fields.
type UpdateInput struct {
	Email              string
	DisplayName        string
	LocalLoginDisabled *bool
}

// UpdateUser edits a user's profile.
func (s *Service) UpdateUser(ctx context.Context, id string, in UpdateInput, actorID string) (*domain.User, error) {
	u, err := s.repo.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	if in.Email != "" {
		u.Email = strings.TrimSpace(in.Email)
	}
	if in.DisplayName != "" {
		u.DisplayName = in.DisplayName
	}
	if in.LocalLoginDisabled != nil {
		u.LocalLoginDisabled = *in.LocalLoginDisabled
	}
	if err := u.Validate(); err != nil {
		return nil, err
	}
	if err := s.repo.UpdateUser(ctx, u); err != nil {
		return nil, fmt.Errorf("user: update: %w", err)
	}
	_ = s.record(ctx, AuditEvent{Action: "user.update", Actor: actorID, Target: id, Result: "success"})
	out := *u
	out.PasswordHash = ""
	return &out, nil
}

// SetDisabled disables/enables a user. Disabling the last active local
// administrator is refused, and disabling revokes the user's sessions.
func (s *Service) SetDisabled(ctx context.Context, id string, disabled bool, actorID string) error {
	u, err := s.repo.GetUser(ctx, id)
	if err != nil {
		return err
	}
	if disabled && u.Disabled == disabled {
		return nil // idempotent
	}
	if disabled {
		last, err := s.isLastActiveAdmin(ctx, id)
		if err != nil {
			return err
		}
		if last {
			return ErrLastAdmin
		}
	}
	u.Disabled = disabled
	if err := s.repo.UpdateUser(ctx, u); err != nil {
		return fmt.Errorf("user: update: %w", err)
	}
	if disabled && s.sessions != nil {
		if err := s.sessions.DeleteUserSessions(ctx, id, ""); err != nil {
			return fmt.Errorf("user: revoke sessions: %w", err)
		}
	}
	detail := "enabled"
	if disabled {
		detail = "disabled"
	}
	return s.record(ctx, AuditEvent{Action: "user.disabled", Actor: actorID, Target: id, Result: "success", Detail: detail})
}

// SetPassword sets a user's password (admin reset) and revokes sessions.
func (s *Service) SetPassword(ctx context.Context, id, newPassword, actorID string) error {
	if len(newPassword) < s.minPass {
		return fmt.Errorf("%w: minimum length %d", ErrWeakPassword, s.minPass)
	}
	u, err := s.repo.GetUser(ctx, id)
	if err != nil {
		return err
	}
	hash, err := s.hash.Hash(newPassword)
	if err != nil {
		return fmt.Errorf("user: password hash: %w", err)
	}
	u.PasswordHash = hash
	u.LocalLoginDisabled = false
	if err := s.repo.UpdateUser(ctx, u); err != nil {
		return fmt.Errorf("user: update: %w", err)
	}
	if s.sessions != nil {
		if err := s.sessions.DeleteUserSessions(ctx, id, ""); err != nil {
			return fmt.Errorf("user: revoke sessions: %w", err)
		}
	}
	return s.record(ctx, AuditEvent{Action: "user.password.reset", Actor: actorID, Target: id, Result: "success"})
}

// ChangePassword verifies the old password before setting a new one.
func (s *Service) ChangePassword(ctx context.Context, id, oldPassword, newPassword string) error {
	if len(newPassword) < s.minPass {
		return fmt.Errorf("%w: minimum length %d", ErrWeakPassword, s.minPass)
	}
	u, err := s.repo.GetUser(ctx, id)
	if err != nil {
		return err
	}
	if u.PasswordHash == "" {
		return errors.New("user: no local password set")
	}
	if err := s.hash.Verify(u.PasswordHash, oldPassword); err != nil {
		return errors.New("user: current password is incorrect")
	}
	hash, err := s.hash.Hash(newPassword)
	if err != nil {
		return fmt.Errorf("user: password hash: %w", err)
	}
	u.PasswordHash = hash
	if err := s.repo.UpdateUser(ctx, u); err != nil {
		return fmt.Errorf("user: update: %w", err)
	}
	if s.sessions != nil {
		if err := s.sessions.DeleteUserSessions(ctx, id, ""); err != nil {
			return fmt.Errorf("user: revoke sessions: %w", err)
		}
	}
	return s.record(ctx, AuditEvent{Action: "user.password.change", Actor: id, Target: id, Result: "success"})
}

// ListRoles returns all roles.
func (s *Service) ListRoles(ctx context.Context) ([]domain.Role, error) {
	return s.repo.ListRoles(ctx)
}

// ListUserRoles returns a user's roles.
func (s *Service) ListUserRoles(ctx context.Context, userID string) ([]domain.Role, error) {
	if _, err := s.repo.GetUser(ctx, userID); err != nil {
		return nil, err
	}
	return s.repo.ListUserRoles(ctx, userID)
}

// GrantRole grants a role by name (idempotent at the repo layer).
func (s *Service) GrantRole(ctx context.Context, userID, roleName, actorID string) error {
	u, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	role, err := s.repo.GetRoleByName(ctx, roleName)
	if err != nil {
		return fmt.Errorf("user: role %q: %w", roleName, err)
	}
	if err := s.repo.GrantRole(ctx, u.ID, role.ID, actorID); err != nil {
		return fmt.Errorf("user: grant role: %w", err)
	}
	return s.record(ctx, AuditEvent{Action: "user.role.grant", Actor: actorID, Target: userID, Result: "success", Detail: "role:" + roleName})
}

// RevokeRole revokes a role by name. Revoking the administrator role from
// the last active administrator is refused.
func (s *Service) RevokeRole(ctx context.Context, userID, roleName, actorID string) error {
	u, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	role, err := s.repo.GetRoleByName(ctx, roleName)
	if err != nil {
		return fmt.Errorf("user: role %q: %w", roleName, err)
	}
	if role.Name == domain.RoleAdministrator {
		last, err := s.isLastActiveAdmin(ctx, userID)
		if err != nil {
			return err
		}
		if last {
			// Refuse only when this user would lose their last admin
			// grant; other roles are unaffected.
			return ErrLastAdmin
		}
	}
	if err := s.repo.RevokeRole(ctx, u.ID, role.ID); err != nil {
		return fmt.Errorf("user: revoke role: %w", err)
	}
	return s.record(ctx, AuditEvent{Action: "user.role.revoke", Actor: actorID, Target: userID, Result: "success", Detail: "role:" + roleName})
}

// isLastActiveAdmin reports whether userID is the sole active administrator.
func (s *Service) isLastActiveAdmin(ctx context.Context, userID string) (bool, error) {
	adminRole, err := s.repo.GetRoleByName(ctx, domain.RoleAdministrator)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("user: admin role lookup: %w", err)
	}
	roles, err := s.repo.ListUserRoles(ctx, userID)
	if err != nil {
		return false, err
	}
	isAdmin := false
	for _, r := range roles {
		if r.ID == adminRole.ID {
			isAdmin = true
			break
		}
	}
	if !isAdmin {
		return false, nil
	}
	const pageSize = 200
	for offset := 0; ; offset += pageSize {
		res, err := s.repo.ListUsers(ctx, domain.ListOptions{Limit: pageSize, Offset: offset})
		if err != nil {
			return false, err
		}
		for _, u := range res.Items {
			if u.ID == userID || u.Disabled {
				continue
			}
			ur, err := s.repo.ListUserRoles(ctx, u.ID)
			if err != nil {
				return false, err
			}
			for _, r := range ur {
				if r.ID == adminRole.ID {
					return false, nil // another active admin exists
				}
			}
		}
		if len(res.Items) < pageSize {
			break
		}
	}
	return true, nil
}

func (s *Service) record(ctx context.Context, e AuditEvent) error {
	if s.audit == nil {
		return nil
	}
	e.At = s.clock.Now()
	return s.audit.RecordUserEvent(ctx, e)
}
