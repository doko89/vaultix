// Package session is a hand-written pgx implementation of
// domain.SessionStore.
//
// DOCUMENTED DEVIATION: the repo convention (TECH_SPEC §3) is pgxpool +
// sqlc-generated queries with no raw SQL in the codebase. This store uses
// hand-written parameterized ($n placeholders only, never interpolated
// values) pgx to avoid sqlc-gen conflicts while the sessions schema is new.
// The platform track should sqlc-ify it once the schema stabilizes: add
// db/queries/sessions.sql mirroring the statements below, run
// sqlc generate, and reimplement the methods on the generated queries.
// All statements use explicit ::uuid casts on UUID parameters and ::text on
// UUID outputs so they behave identically under sqlc codegen.
package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vaultix/vaultix/internal/domain"
)

// queryTimeout bounds every session statement (TECH_SPEC §3).
const queryTimeout = 5 * time.Second

// querier is the pgx surface Store needs. *pgxpool.Pool satisfies it; tests
// substitute a fake to assert SQL shape and argument mapping.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Store implements domain.SessionStore over pgx.
type Store struct {
	db querier
}

// New builds a Store on a live pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{db: pool}
}

var _ domain.SessionStore = (*Store)(nil)

const (
	colList = "id::text, user_id::text, token_hash, " +
		"oidc_provider_id::text, COALESCE(id_token,''), " +
		"COALESCE(ip,''), COALESCE(user_agent,''), " +
		"created_at, expires_at, last_seen_at"

	insertSession = `INSERT INTO sessions
		(user_id, token_hash, oidc_provider_id, id_token, ip, user_agent, expires_at, last_seen_at)
		VALUES ($1::uuid, $2, NULLIF($3,'')::uuid, NULLIF($4,''), $5, $6, $7, $8)
		RETURNING ` + colList

	selectByTokenHash = `SELECT ` + colList + `
		FROM sessions WHERE token_hash = $1`

	touchSession = `UPDATE sessions SET last_seen_at = $2 WHERE id = $1::uuid`

	deleteSession = `DELETE FROM sessions WHERE id = $1::uuid`

	deleteByTokenHash = `DELETE FROM sessions WHERE token_hash = $1`

	deleteUserSessionsExcept = `DELETE FROM sessions
		WHERE user_id = $1::uuid AND ($2 = '' OR id::text <> $2)`

	deleteExpired = `DELETE FROM sessions WHERE expires_at <= $1`
)

// Create inserts a session; the row ID and CreatedAt are populated back.
func (s *Store) Create(ctx context.Context, sess *domain.Session) error {
	if sess == nil {
		return fmt.Errorf("session: %w", domain.ErrValidation)
	}
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	prov := ""
	if sess.OIDCProviderID != "" {
		prov = sess.OIDCProviderID
	}
	row := s.db.QueryRow(ctx, insertSession,
		sess.UserID, sess.TokenHash, prov, sess.IDToken,
		truncate(sess.IP, 64), truncate(sess.UserAgent, 512),
		sess.ExpiresAt, sess.LastSeenAt,
	)
	var out domain.Session
	var provOut *string
	if err := row.Scan(&out.ID, &out.UserID, &out.TokenHash,
		&provOut, &out.IDToken, &out.IP, &out.UserAgent,
		&out.CreatedAt, &out.ExpiresAt, &out.LastSeenAt,
	); err != nil {
		return fmt.Errorf("session: create: %w", mapErr(err))
	}
	if provOut != nil {
		out.OIDCProviderID = *provOut
	}
	*sess = out
	return nil
}

// GetByTokenHash fetches a session by token hash.
func (s *Store) GetByTokenHash(ctx context.Context, tokenHash string) (*domain.Session, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	row := s.db.QueryRow(ctx, selectByTokenHash, tokenHash)
	var out domain.Session
	var provOut *string
	if err := row.Scan(&out.ID, &out.UserID, &out.TokenHash,
		&provOut, &out.IDToken, &out.IP, &out.UserAgent,
		&out.CreatedAt, &out.ExpiresAt, &out.LastSeenAt,
	); err != nil {
		return nil, fmt.Errorf("session: lookup: %w", mapErr(err))
	}
	if provOut != nil {
		out.OIDCProviderID = *provOut
	}
	return &out, nil
}

// Touch refreshes LastSeenAt for idle-timeout tracking.
func (s *Store) Touch(ctx context.Context, id string, at time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	tag, err := s.db.Exec(ctx, touchSession, id, at)
	if err != nil {
		return fmt.Errorf("session: touch: %w", mapErr(err))
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("session: touch: %w", domain.ErrNotFound)
	}
	return nil
}

// Delete removes a session by ID.
func (s *Store) Delete(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	if _, err := s.db.Exec(ctx, deleteSession, id); err != nil {
		return fmt.Errorf("session: delete: %w", mapErr(err))
	}
	return nil
}

// DeleteByTokenHash removes a session by token hash (logout path).
func (s *Store) DeleteByTokenHash(ctx context.Context, tokenHash string) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	if _, err := s.db.Exec(ctx, deleteByTokenHash, tokenHash); err != nil {
		return fmt.Errorf("session: delete: %w", mapErr(err))
	}
	return nil
}

// DeleteUserSessions revokes a user's sessions, optionally keeping one.
func (s *Store) DeleteUserSessions(ctx context.Context, userID, exceptID string) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	if _, err := s.db.Exec(ctx, deleteUserSessionsExcept, userID, exceptID); err != nil {
		return fmt.Errorf("session: delete user sessions: %w", mapErr(err))
	}
	return nil
}

// DeleteExpired prunes absolutely-expired sessions.
func (s *Store) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	tag, err := s.db.Exec(ctx, deleteExpired, now)
	if err != nil {
		return 0, fmt.Errorf("session: prune: %w", mapErr(err))
	}
	return tag.RowsAffected(), nil
}

func mapErr(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return domain.ErrConflict
		case "23503", "22P02": // fk_violation, invalid_text_representation
			return domain.ErrValidation
		}
	}
	return err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
