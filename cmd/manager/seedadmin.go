package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/vaultix/vaultix/internal/adapter/repo"
	"github.com/vaultix/vaultix/internal/domain"
	authuc "github.com/vaultix/vaultix/internal/usecase/auth"
	useruc "github.com/vaultix/vaultix/internal/usecase/user"
)

// seed-admin bootstraps the first Manager administrator for E2E setups:
// it creates one local user with the administrator role. Credentials arrive
// via VAULTIX_ADMIN_USER / VAULTIX_ADMIN_PASSWORD (or flags); the password
// is never printed, logged, or echoed back.

// Environment overrides for seed-admin (flags win when both are set).
const (
	seedAdminUserEnv     = "VAULTIX_ADMIN_USER"
	seedAdminPasswordEnv = "VAULTIX_ADMIN_PASSWORD"
	seedAdminEmailEnv    = "VAULTIX_ADMIN_EMAIL"
)

// errSeedAdminRefused reports that users already exist (use --force to add
// another administrator anyway).
var errSeedAdminRefused = errors.New("seed-admin: users already exist (pass --force to create another administrator)")

// seedAdminOptions carries resolved seed-admin inputs.
type seedAdminOptions struct {
	username string
	password string
	email    string
	force    bool
}

// resolveSeedAdminOptions merges flags over environment. Empty email
// defaults to username@localhost so bootstrap never blocks on a mailbox.
func resolveSeedAdminOptions(username, password, email string, force bool) seedAdminOptions {
	if username == "" {
		username = strings.TrimSpace(os.Getenv(seedAdminUserEnv))
	}
	if password == "" {
		password = os.Getenv(seedAdminPasswordEnv)
	}
	if email == "" {
		email = strings.TrimSpace(os.Getenv(seedAdminEmailEnv))
	}
	username = strings.TrimSpace(username)
	email = strings.TrimSpace(email)
	if email == "" && username != "" {
		email = username + "@localhost"
	}
	return seedAdminOptions{username: username, password: password, email: email, force: force}
}

// runSeedAdmin creates the bootstrap administrator through the user service
// (bcrypt hashing, role grant, break-glass guards). It refuses when any user
// exists unless force is set.
func runSeedAdmin(ctx context.Context, users *useruc.Service, opts seedAdminOptions) (*domain.User, error) {
	if strings.TrimSpace(opts.username) == "" {
		return nil, fmt.Errorf("seed-admin: username is required (flag --username or env %s)", seedAdminUserEnv)
	}
	if opts.password == "" {
		return nil, fmt.Errorf("seed-admin: password is required (flag --password or env %s)", seedAdminPasswordEnv)
	}
	if strings.TrimSpace(opts.email) == "" {
		opts.email = strings.TrimSpace(opts.username) + "@localhost"
	}
	existing, err := users.ListUsers(ctx, domain.ListOptions{Limit: 1})
	if err != nil {
		return nil, fmt.Errorf("seed-admin: list users: %w", err)
	}
	if existing.Total > 0 && !opts.force {
		return nil, errSeedAdminRefused
	}
	u, err := users.CreateUser(ctx, useruc.CreateInput{
		Username: opts.username, Email: opts.email, Password: opts.password,
		RoleNames: []string{domain.RoleAdministrator},
	}, "")
	if err != nil {
		return nil, fmt.Errorf("seed-admin: create administrator: %w", err)
	}
	return u, nil
}

func newSeedAdminCommand(configPath *string) *cobra.Command {
	var username, password, email string
	var force bool
	cmd := &cobra.Command{
		Use:   "seed-admin",
		Short: "Create the initial administrator user (E2E bootstrap)",
		Long: `Create a local user with the administrator role.

Credentials come from flags (--username/--password/--email) or the
VAULTIX_ADMIN_USER / VAULTIX_ADMIN_PASSWORD / VAULTIX_ADMIN_EMAIL
environment variables. The command refuses when any user already exists
unless --force is passed. The password is never printed.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, db, err := openMigrationDB(cmd, *configPath)
			if err != nil {
				return err
			}
			defer db.pool.Close()
			if err := db.pool.Ping(cmd.Context()); err != nil {
				return fmt.Errorf("ping database: %w", err)
			}
			return runSeedAdminDB(cmd, db.pool, resolveSeedAdminOptions(username, password, email, force))
		},
	}
	registerFlags(cmd, false)
	cmd.Flags().StringVar(&username, "username", "", "admin username (env "+seedAdminUserEnv+")")
	cmd.Flags().StringVar(&password, "password", "", "admin password (env "+seedAdminPasswordEnv+")")
	cmd.Flags().StringVar(&email, "email", "", "admin email, defaults to <username>@localhost (env "+seedAdminEmailEnv+")")
	cmd.Flags().BoolVar(&force, "force", false, "create even when users already exist")
	return cmd
}

// runSeedAdminDB wires the user service over pgx and prints next steps. The
// password never appears in output.
func runSeedAdminDB(cmd *cobra.Command, pool *pgxpool.Pool, opts seedAdminOptions) error {
	users, err := useruc.NewService(useruc.Deps{Repo: repo.New(pool).Users, Hash: authuc.BcryptHasher{}})
	if err != nil {
		return err
	}
	u, err := runSeedAdmin(cmd.Context(), users, opts)
	if err != nil {
		return err
	}
	cmd.Printf("seed-admin: created administrator %q (id %s)\n", u.Username, u.ID)
	cmd.Println("next steps:")
	cmd.Println("  1. start the manager: ./manager serve")
	cmd.Println("  2. log in at the web UI with the seeded username and password")
	cmd.Println("  3. rotate the bootstrap password (Profile > Change password)")
	cmd.Println("  4. unset VAULTIX_ADMIN_PASSWORD so the bootstrap secret stops circulating")
	return nil
}
