package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resetViper mirrors production boot: fresh viper with the real command
// flags bound (env vars only unmarshal for bound/defaulted keys).
func resetViper(t *testing.T) {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	var configPath string
	bindRunFlags(newServeCommand(&configPath))
}

func TestConfigDefaults(t *testing.T) {
	resetViper(t)
	cfg, err := loadConfig("")
	require.NoError(t, err)
	assert.Equal(t, ":8080", cfg.Addr)
	assert.Equal(t, "db/migrations", cfg.MigrationsDir)
	assert.Equal(t, "./web/dist", cfg.StaticDir)
	assert.Equal(t, "info", cfg.LogLevel)
	assert.Equal(t, 30, cfg.RequestTimeoutSecs)
	assert.Error(t, cfg.Validate(), "database_url is required")
}

func TestConfigEnv(t *testing.T) {
	resetViper(t)
	t.Setenv("VAULTIX_ADDR", ":9090")
	t.Setenv("VAULTIX_DATABASE_URL", "postgres://u:p@localhost:5432/v?sslmode=disable")
	t.Setenv("VAULTIX_LOG_LEVEL", "debug")
	t.Setenv("VAULTIX_S3_ENDPOINT", "http://minio:9000")
	t.Setenv("VAULTIX_S3_BUCKET", "backups")
	t.Setenv("VAULTIX_S3_SECRET_KEY", "shh")
	t.Setenv("VAULTIX_SMTP_HOST", "mail.example")
	t.Setenv("VAULTIX_OIDC_REDIRECT_URL", "https://vaultix.example/api/auth/oidc/callback")
	cfg, err := loadConfig("")
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())
	assert.Equal(t, ":9090", cfg.Addr)
	assert.Equal(t, "debug", cfg.LogLevel)
	assert.True(t, cfg.S3.Configured())
	assert.Equal(t, "mail.example", cfg.SMTP.Host)
	assert.Equal(t, "https://vaultix.example/api/auth/oidc/callback", cfg.OIDCRedirectURL)
}

func TestConfigFile(t *testing.T) {
	resetViper(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "manager.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
addr: ":7070"
database_url: postgres://u:p@localhost:5432/v?sslmode=disable
log_level: warn
s3:
  endpoint: http://minio:9000
  bucket: b
smtp:
  host: mail.example
  port: 587
`), 0o644))
	cfg, err := loadConfig(path)
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())
	assert.Equal(t, ":7070", cfg.Addr)
	assert.Equal(t, "warn", cfg.LogLevel)
	assert.Equal(t, 587, cfg.SMTP.Port)
}

func TestFlagBeatsEnv(t *testing.T) {
	resetViper(t)
	t.Setenv("VAULTIX_ADDR", ":9090")
	var configPath string
	cmd := newServeCommand(&configPath)
	require.NoError(t, cmd.ParseFlags([]string{"--addr", ":1234"}))
	bindRunFlags(cmd)
	cfg, err := loadConfig("")
	require.NoError(t, err)
	assert.Equal(t, ":1234", cfg.Addr)
}

func TestConfigMissingExplicitFileErrors(t *testing.T) {
	resetViper(t)
	_, err := loadConfig(filepath.Join(t.TempDir(), "nope.yaml"))
	require.Error(t, err)
}

func TestConfigRedacted(t *testing.T) {
	cfg := Config{
		DatabaseURL: "postgres://vaultix:s3cr3t-pw@db:5432/v?sslmode=disable",
		S3:          S3Config{SecretKey: "minio-secret"},
		SMTP:        SMTPConfig{Password: "mail-pw"},
	}
	red := cfg.Redacted()
	assert.NotContains(t, red.DatabaseURL, "s3cr3t-pw")
	assert.Contains(t, red.DatabaseURL, "vaultix")
	assert.Equal(t, "***", red.S3.SecretKey)
	assert.Equal(t, "***", red.SMTP.Password)
	// Original untouched.
	assert.Contains(t, cfg.DatabaseURL, "s3cr3t-pw")
}

func TestConfigValidate(t *testing.T) {
	assert.Error(t, Config{}.Validate())
	assert.Error(t, Config{DatabaseURL: "x", LogLevel: "verbose"}.Validate())
	assert.NoError(t, Config{DatabaseURL: "x"}.Validate())
}

// ---- migration runner with a fake DB ----

type fakeRows struct {
	rows [][]any
	i    int
}

func (r *fakeRows) Next() bool { return r.i < len(r.rows) }
func (r *fakeRows) Scan(dest ...any) error {
	if r.i >= len(r.rows) {
		return fmt.Errorf("no rows")
	}
	for i, d := range dest {
		switch p := d.(type) {
		case *int64:
			*p = r.rows[r.i][i].(int64)
		case *bool:
			*p = r.rows[r.i][i].(bool)
		default:
			return fmt.Errorf("unsupported scan type")
		}
	}
	r.i++
	return nil
}
func (r *fakeRows) Err() error { return nil }
func (r *fakeRows) Close()     {}

type fakeDB struct {
	applied map[int64]bool
	stmts   []string
	// failOn makes Exec fail when the statement contains the substring.
	failOn string
	inTx   bool
}

func newFakeDB() *fakeDB { return &fakeDB{applied: map[int64]bool{}} }

func (f *fakeDB) Exec(_ context.Context, sql string, args ...any) error {
	return f.exec(sql, args, &f.applied, &f.stmts)
}

func (f *fakeDB) exec(sql string, args []any, applied *map[int64]bool, stmts *[]string) error {
	if f.failOn != "" && strings.Contains(sql, f.failOn) {
		return fmt.Errorf("boom: %s", f.failOn)
	}
	upper := strings.ToUpper(strings.TrimSpace(sql))
	switch {
	case strings.HasPrefix(upper, "INSERT INTO GOOSE_DB_VERSION"):
		(*applied)[args[0].(int64)] = args[1].(bool)
	case strings.HasPrefix(upper, "DELETE FROM GOOSE_DB_VERSION"):
		delete(*applied, args[0].(int64))
	case strings.HasPrefix(upper, "CREATE TABLE") && strings.Contains(upper, "GOOSE_DB_VERSION"):
		// version table creation; no-op for the fake
	default:
		*stmts = append(*stmts, sql)
	}
	return nil
}

func (f *fakeDB) Query(_ context.Context, sql string, _ ...any) (Rows, error) {
	if !strings.Contains(strings.ToUpper(sql), "GOOSE_DB_VERSION") {
		return nil, fmt.Errorf("fake supports only version queries")
	}
	var rows [][]any
	for v, ok := range f.applied {
		rows = append(rows, []any{v, ok})
	}
	return &fakeRows{rows: rows}, nil
}

func (f *fakeDB) Begin(context.Context) (Tx, error) {
	cp := map[int64]bool{}
	for k, v := range f.applied {
		cp[k] = v
	}
	return &fakeTx{db: f, applied: cp}, nil
}

type fakeTx struct {
	db      *fakeDB
	applied map[int64]bool
	stmts   []string
	done    bool
}

func (t *fakeTx) Exec(ctx context.Context, sql string, args ...any) error {
	return t.db.exec(sql, args, &t.applied, &t.stmts)
}

func (t *fakeTx) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	return t.db.Query(ctx, sql, args...)
}

func (t *fakeTx) Commit(context.Context) error {
	t.db.applied = t.applied
	t.db.stmts = append(t.db.stmts, t.stmts...)
	t.done = true
	return nil
}

func (t *fakeTx) Rollback(context.Context) error { t.done = true; return nil }

func writeMigration(t *testing.T, dir, name, up, down string) {
	t.Helper()
	content := "-- test migration\n\n-- +goose Up\n" + up + "\n-- +goose Down\n" + down + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}

func TestRunnerUpDownStatus(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "00001_a.sql", "CREATE TABLE a (id INT);", "DROP TABLE a;")
	writeMigration(t, dir, "00002_b.sql", "CREATE TABLE b (id INT);", "DROP TABLE b;")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("ignored"), 0o644))

	ctx := context.Background()
	db := newFakeDB()
	r := Runner{}

	applied, err := r.Up(ctx, db, dir)
	require.NoError(t, err)
	require.Len(t, applied, 2)
	assert.Equal(t, int64(1), applied[0].Version)
	assert.Equal(t, int64(2), applied[1].Version)
	assert.Len(t, db.stmts, 2, "one statement per migration")

	// Idempotent: second Up applies nothing.
	applied, err = r.Up(ctx, db, dir)
	require.NoError(t, err)
	assert.Empty(t, applied)

	status, err := r.Status(ctx, db, dir)
	require.NoError(t, err)
	require.Len(t, status, 2)
	assert.True(t, status[0].Applied)
	assert.True(t, status[1].Applied)

	// Down reverts the latest only.
	reverted, err := r.Down(ctx, db, dir)
	require.NoError(t, err)
	assert.Equal(t, int64(2), reverted.Version)
	status, err = r.Status(ctx, db, dir)
	require.NoError(t, err)
	assert.True(t, status[0].Applied)
	assert.False(t, status[1].Applied)

	_, err = r.Down(ctx, db, dir)
	require.NoError(t, err)
	_, err = r.Down(ctx, db, dir)
	require.ErrorContains(t, err, "no applied migration")
}

func TestRunnerRollsBackFailedMigration(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "00001_ok.sql", "CREATE TABLE ok (id INT);", "DROP TABLE ok;")
	writeMigration(t, dir, "00002_bad.sql", "CREATE TABLE bad (id INT); FAIL STATEMENT;", "DROP TABLE bad;")

	ctx := context.Background()
	db := newFakeDB()
	db.failOn = "FAIL STATEMENT"
	_, err := Runner{}.Up(ctx, db, dir)
	require.Error(t, err)
	assert.True(t, db.applied[1], "first migration stays applied")
	assert.False(t, db.applied[2], "failed migration must not be recorded")
	for _, s := range db.stmts {
		assert.NotContains(t, s, "CREATE TABLE bad", "failed tx statements must roll back")
	}
}

func TestCollectRealMigrations(t *testing.T) {
	migrations, err := Runner{}.Collect("../../db/migrations")
	require.NoError(t, err)
	// Phase 2 merge: the auth track adds 00010_sessions.sql on top of the
	// base 9, and integration adds 00011 (nullable credential agent_id).
	// Phase 3 db cleanup adds 00012 (application stacks).
	require.Len(t, migrations, 12, "base schema has 12 migrations")
	for i, m := range migrations {
		assert.Equal(t, int64(i+1), m.Version)
		assert.NotEmpty(t, m.Up, "migration %d needs Up statements", m.Version)
		assert.NotEmpty(t, m.Down, "migration %d needs Down statements", m.Version)
	}
}

func TestSplitStatements(t *testing.T) {
	stmts := splitStatements(`CREATE TABLE a (id INT);
INSERT INTO a VALUES ('semi;colon', 'it''s');
-- comment; with semicolon
CREATE TABLE b (id INT);`)
	require.Len(t, stmts, 3)
	assert.Contains(t, stmts[1], "semi;colon")

	funcs := splitStatements(`CREATE FUNCTION f() RETURNS void AS $$
BEGIN
  RAISE NOTICE 'hi;';
END;
$$ LANGUAGE plpgsql;
SELECT 1;`)
	require.Len(t, funcs, 2, "dollar-quoted bodies must not split")
}

func TestSplitGooseSectionsRequiresBoth(t *testing.T) {
	_, _, err := splitGooseSections("SELECT 1;\n")
	require.ErrorContains(t, err, "Up")
	_, _, err = splitGooseSections("-- +goose Up\nSELECT 1;\n")
	require.ErrorContains(t, err, "Down")
	up, down, err := splitGooseSections("-- +goose Up\nSELECT 1;\n-- +goose Down\nSELECT 2;\n")
	require.NoError(t, err)
	assert.Contains(t, up, "SELECT 1")
	assert.Contains(t, down, "SELECT 2")
}

func TestVersionCommand(t *testing.T) {
	cmd := newVersionCommand()
	cmd.SetOut(os.Stdout)
	require.NoError(t, cmd.Execute())
}

func TestRunServeValidationErrors(t *testing.T) {
	resetViper(t)
	err := runServe(context.Background(), "", false)
	require.ErrorContains(t, err, "database_url is required")

	resetViper(t)
	t.Setenv("VAULTIX_DATABASE_URL", "postgres://u:p@localhost:5432/v?sslmode=disable")
	t.Setenv("VAULTIX_LOG_LEVEL", "verbose")
	err = runServe(context.Background(), "", false)
	require.ErrorContains(t, err, "log_level")
}

func TestRunServeUnreachableDB(t *testing.T) {
	resetViper(t)
	t.Setenv("VAULTIX_DATABASE_URL", "postgres://u:p@127.0.0.1:1/v?sslmode=disable&connect_timeout=2")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := runServe(ctx, "", true) // skip migrations: fail at ping
	require.ErrorContains(t, err, "ping database")
}

func TestOpenMigrationDBValidationError(t *testing.T) {
	resetViper(t)
	var configPath string
	_, _, err := openMigrationDB(newMigrateCommand(&configPath), "")
	require.ErrorContains(t, err, "database_url is required")
}

func TestTimeoutHelpers(t *testing.T) {
	assert.Equal(t, 30*time.Second, Config{}.RequestTimeout())
	assert.Equal(t, 5*time.Second, Config{RequestTimeoutSecs: 5}.RequestTimeout())
	assert.Equal(t, 15*time.Second, Config{}.ShutdownTimeout())
	assert.Equal(t, 60*time.Second, Config{ShutdownTimeoutSecs: 60}.ShutdownTimeout())
}

func TestNewLoggerLevels(t *testing.T) {
	for _, level := range []string{"debug", "info", "warn", "error", "", "bogus"} {
		assert.NotNil(t, newLogger(level))
	}
}

func TestSplitStatementsPlaceholders(t *testing.T) {
	// $1-style placeholders must not open dollar-quoted strings.
	stmts := splitStatements(`INSERT INTO goose_db_version (version_id, is_applied) VALUES ($1, $2); SELECT 1;`)
	require.Len(t, stmts, 2)
	// Unterminated dollar quote consumes the rest (garbage-in, no crash).
	stmts = splitStatements(`SELECT $$unterminated`)
	require.Len(t, stmts, 1)
	// Unterminated block comment and line comment at EOF.
	stmts = splitStatements(`SELECT 1; /* trailing`)
	require.Len(t, stmts, 1)
	stmts = splitStatements("SELECT 1; -- trailing")
	require.Len(t, stmts, 1)
}

func TestRootCommandWiring(t *testing.T) {
	root := newRootCommand()
	names := map[string]bool{}
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	assert.True(t, names["serve"])
	assert.True(t, names["migrate"])
	assert.True(t, names["version"])
}
