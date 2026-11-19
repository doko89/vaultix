package session

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vaultix/vaultix/internal/domain"
)

// fakeQuerier records statements and serves scripted rows.
type fakeQuerier struct {
	lastSQL  string
	lastArgs []any
	rows     [][]any // queued single-row results for QueryRow
	scanErr  error
	tag      pgconn.CommandTag
	execErr  error
}

func (f *fakeQuerier) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.lastSQL, f.lastArgs = sql, args
	return f.tag, f.execErr
}

func (f *fakeQuerier) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	f.lastSQL, f.lastArgs = sql, args
	return &fakeRow{cols: f.rows[0], err: f.scanErr}
}

type fakeRow struct {
	cols []any
	err  error
}

func (r *fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i := range dest {
		switch d := dest[i].(type) {
		case *string:
			*d = r.cols[i].(string)
		case **string:
			if r.cols[i] == nil {
				*d = nil
			} else {
				s := r.cols[i].(string)
				*d = &s
			}
		case *time.Time:
			*d = r.cols[i].(time.Time)
		default:
			panic("fakeRow: unsupported dest type")
		}
	}
	return nil
}

func sessionRow(id, userID, hash, provID string) []any {
	now := time.Now().UTC()
	var prov any
	if provID != "" {
		prov = provID
	}
	return []any{id, userID, hash, prov, "", "127.0.0.1", "test-agent", now, now.Add(time.Hour), now}
}

// TestStatementsAreParameterized guards the sqlc-deviation contract: no
// value may be interpolated into SQL; only $n placeholders.
func TestStatementsAreParameterized(t *testing.T) {
	for name, stmt := range map[string]string{
		"insert": insertSession, "select": selectByTokenHash, "touch": touchSession,
		"delete": deleteSession, "deleteByHash": deleteByTokenHash,
		"deleteUser": deleteUserSessionsExcept, "deleteExpired": deleteExpired,
	} {
		if strings.Contains(stmt, "'%") || strings.Contains(stmt, "\"+") || strings.Contains(stmt, "fmt.") {
			t.Fatalf("%s statement looks interpolated: %q", name, stmt)
		}
		if !strings.Contains(stmt, "$1") {
			t.Fatalf("%s statement has no placeholders: %q", name, stmt)
		}
	}
}

func TestCreateMapsArgsAndRow(t *testing.T) {
	fq := &fakeQuerier{rows: [][]any{sessionRow("sid", "uid", "hash", "")}}
	s := &Store{db: fq}
	sess := &domain.Session{
		UserID: "uid", TokenHash: "hash",
		ExpiresAt: time.Now().Add(time.Hour), LastSeenAt: time.Now(),
		IP: "1.2.3.4", UserAgent: "ua",
	}
	if err := s.Create(context.Background(), sess); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(fq.lastArgs) != 8 {
		t.Fatalf("expected 8 args, got %d", len(fq.lastArgs))
	}
	if fq.lastArgs[0] != "uid" || fq.lastArgs[1] != "hash" {
		t.Fatalf("arg mismatch: %v", fq.lastArgs)
	}
	if sess.ID != "sid" {
		t.Fatalf("ID not populated: %+v", sess)
	}
}

func TestCreateTruncatesLongFields(t *testing.T) {
	fq := &fakeQuerier{rows: [][]any{sessionRow("sid", "uid", "hash", "")}}
	s := &Store{db: fq}
	sess := &domain.Session{
		UserID: "uid", TokenHash: "hash",
		ExpiresAt: time.Now().Add(time.Hour), LastSeenAt: time.Now(),
		IP: strings.Repeat("9", 200), UserAgent: strings.Repeat("u", 2000),
	}
	if err := s.Create(context.Background(), sess); err != nil {
		t.Fatal(err)
	}
	if len(fq.lastArgs[4].(string)) > 64 || len(fq.lastArgs[5].(string)) > 512 {
		t.Fatalf("fields not truncated: %d/%d", len(fq.lastArgs[4].(string)), len(fq.lastArgs[5].(string)))
	}
}

func TestCreateNilSession(t *testing.T) {
	s := &Store{db: &fakeQuerier{}}
	if err := s.Create(context.Background(), nil); err == nil {
		t.Fatal("Create(nil) must fail")
	}
}

func TestGetByTokenHashNotFound(t *testing.T) {
	fq := &fakeQuerier{rows: [][]any{sessionRow("", "", "", "")}, scanErr: pgx.ErrNoRows}
	s := &Store{db: fq}
	_, err := s.GetByTokenHash(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), domain.ErrNotFound.Error()) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if fq.lastArgs[0] != "missing" {
		t.Fatalf("hash not passed through: %v", fq.lastArgs)
	}
}

func TestGetByTokenHashMapsProvider(t *testing.T) {
	fq := &fakeQuerier{rows: [][]any{sessionRow("sid", "uid", "hash", "prov-1")}}
	s := &Store{db: fq}
	got, err := s.GetByTokenHash(context.Background(), "hash")
	if err != nil {
		t.Fatal(err)
	}
	if got.OIDCProviderID != "prov-1" || got.UserID != "uid" {
		t.Fatalf("row mismatch: %+v", got)
	}
}

func TestTouchRequiresRow(t *testing.T) {
	fq := &fakeQuerier{tag: pgconn.NewCommandTag("UPDATE 0")}
	s := &Store{db: fq}
	err := s.Touch(context.Background(), "sid", time.Now())
	if err == nil || !strings.Contains(err.Error(), domain.ErrNotFound.Error()) {
		t.Fatalf("Touch on missing row must return ErrNotFound, got %v", err)
	}
	fq.tag = pgconn.NewCommandTag("UPDATE 1")
	if err := s.Touch(context.Background(), "sid", time.Now()); err != nil {
		t.Fatalf("Touch: %v", err)
	}
}

func TestDeleteVariants(t *testing.T) {
	fq := &fakeQuerier{}
	s := &Store{db: fq}
	ctx := context.Background()
	if err := s.Delete(ctx, "sid"); err != nil {
		t.Fatal(err)
	}
	if fq.lastArgs[0] != "sid" {
		t.Fatalf("Delete arg: %v", fq.lastArgs)
	}
	if err := s.DeleteByTokenHash(ctx, "h"); err != nil {
		t.Fatal(err)
	}
	if fq.lastArgs[0] != "h" {
		t.Fatalf("DeleteByTokenHash arg: %v", fq.lastArgs)
	}
	if err := s.DeleteUserSessions(ctx, "u", "keep"); err != nil {
		t.Fatal(err)
	}
	if fq.lastArgs[0] != "u" || fq.lastArgs[1] != "keep" {
		t.Fatalf("DeleteUserSessions args: %v", fq.lastArgs)
	}
	fq.tag = pgconn.NewCommandTag("DELETE 3")
	n, err := s.DeleteExpired(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("DeleteExpired count = %d, want 3", n)
	}
}

func TestMapErrConflictAndValidation(t *testing.T) {
	conflict := &pgconn.PgError{Code: "23505"}
	if got := mapErr(conflict); got != domain.ErrConflict {
		t.Fatalf("23505 -> %v, want ErrConflict", got)
	}
	for _, code := range []string{"23503", "22P02"} {
		if got := mapErr(&pgconn.PgError{Code: code}); got != domain.ErrValidation {
			t.Fatalf("%s -> %v, want ErrValidation", code, got)
		}
	}
}

// TestLivePostgres exercises the real statements when a database is
// available: TEST_POSTGRES_DSN=postgres://... go test ./internal/adapter/session/
// It round-trips a session row (plus a throwaway users row for the FK).
func TestLivePostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN unset; skipping live postgres test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	var hasSessions, hasUsers bool
	_ = pool.QueryRow(ctx, `SELECT to_regclass('sessions') IS NOT NULL`).Scan(&hasSessions)
	_ = pool.QueryRow(ctx, `SELECT to_regclass('users') IS NOT NULL`).Scan(&hasUsers)
	if !hasSessions || !hasUsers {
		t.Skip("sessions/users tables missing; run migrations first")
	}
	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name) VALUES ($1, 'sess-test') RETURNING id::text`,
		"sesstest+"+time.Now().Format("150405.000000")+t.Name()+`@example.com`,
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	defer pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, userID)

	s := New(pool)
	now := time.Now().UTC()
	sess := &domain.Session{
		UserID: userID, TokenHash: "livetest-" + now.Format("150405.000000"),
		IP: "10.0.0.1", UserAgent: "live-test",
		ExpiresAt: now.Add(time.Hour), LastSeenAt: now,
	}
	if err := s.Create(ctx, sess); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sess.ID == "" {
		t.Fatal("Create did not populate ID")
	}
	got, err := s.GetByTokenHash(ctx, sess.TokenHash)
	if err != nil {
		t.Fatalf("GetByTokenHash: %v", err)
	}
	if got.UserID != userID {
		t.Fatalf("UserID = %q, want %q", got.UserID, userID)
	}
	later := now.Add(5 * time.Minute)
	if err := s.Touch(ctx, sess.ID, later); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	got, _ = s.GetByTokenHash(ctx, sess.TokenHash)
	if got.LastSeenAt.Before(later.Add(-time.Minute)) {
		t.Fatalf("Touch not persisted: %v", got.LastSeenAt)
	}
	if err := s.Delete(ctx, sess.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.GetByTokenHash(ctx, sess.TokenHash); !errors.Is(err, domain.ErrNotFound) &&
		!strings.Contains(err.Error(), domain.ErrNotFound.Error()) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}
