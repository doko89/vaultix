package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

// This file implements `manager migrate` (up/down/status) with a minimal
// goose-compatible runner over db/migrations/*.sql. go.mod is frozen for
// this track, so github.com/pressly/goose/v3 cannot be imported; instead the
// runner mirrors goose v3 semantics exactly (verified against goose v3.30.1
// sources):
//
//   - version table: goose_db_version(id, version_id, is_applied, tstamp)
//   - up:   run Up statements + INSERT (version, true), one tx per migration
//   - down: run Down statements of the latest applied + DELETE its row, in tx
//   - applied set: rows present in goose_db_version (latest row per version)
//
// A database migrated by this runner is indistinguishable from one migrated
// by the goose CLI (and vice versa), so `make migrate-up/down` and
// `manager migrate` interoperate. Integrator-2 may swap this file for goose
// once the dependency lands; the cobra surface stays the same.

const versionTable = "goose_db_version"

var migrationFileRe = regexp.MustCompile(`^([0-9]+)_(.+)\.sql$`)

// Migration is one parsed *.sql file.
type Migration struct {
	Version int64
	Name    string
	Path    string
	Up      []string
	Down    []string
}

// Rows is the minimal result-set surface the runner needs. pgx.Rows
// satisfies it, so the production adapter passes pgx rows through.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close()
}

// Tx is one migration transaction.
type Tx interface {
	Exec(ctx context.Context, sql string, args ...any) error
	Query(ctx context.Context, sql string, args ...any) (Rows, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// DB is the runner's database seam: Exec/Query plus transactions.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) error
	Query(ctx context.Context, sql string, args ...any) (Rows, error)
	Begin(ctx context.Context) (Tx, error)
}

// pgxDB adapts *pgxpool.Pool to DB.
type pgxDB struct{ pool *pgxpool.Pool }

func (d pgxDB) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := d.pool.Exec(ctx, sql, args...)
	return err
}

func (d pgxDB) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	return d.pool.Query(ctx, sql, args...)
}

func (d pgxDB) Begin(ctx context.Context) (Tx, error) {
	tx, err := d.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return pgxTx{tx: tx}, nil
}

// pgxTx adapts pgx.Tx to Tx.
type pgxTx struct{ tx pgx.Tx }

func (t pgxTx) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := t.tx.Exec(ctx, sql, args...)
	return err
}

func (t pgxTx) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	return t.tx.Query(ctx, sql, args...)
}

func (t pgxTx) Commit(ctx context.Context) error   { return t.tx.Commit(ctx) }
func (t pgxTx) Rollback(ctx context.Context) error { return t.tx.Rollback(ctx) }

// Runner applies migrations from a directory.
type Runner struct {
	Log *slog.Logger
}

func (r Runner) log() *slog.Logger {
	if r.Log == nil {
		return slog.Default()
	}
	return r.Log
}

// Collect parses every migration file in dir, sorted by version.
func (r Runner) Collect(dir string) ([]Migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations dir %s: %w", dir, err)
	}
	var out []Migration
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationFileRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue // ignore non-migration files (README, .keep, ...)
		}
		version, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("migration %s: bad version: %w", e.Name(), err)
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		up, down, err := splitGooseSections(string(data))
		if err != nil {
			return nil, fmt.Errorf("migration %s: %w", e.Name(), err)
		}
		out = append(out, Migration{
			Version: version,
			Name:    m[2],
			Path:    filepath.Join(dir, e.Name()),
			Up:      splitStatements(up),
			Down:    splitStatements(down),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i := 1; i < len(out); i++ {
		if out[i].Version == out[i-1].Version {
			return nil, fmt.Errorf("duplicate migration version %d (%s and %s)",
				out[i].Version, out[i-1].Path, out[i].Path)
		}
	}
	return out, nil
}

// splitGooseSections splits file content on the -- +goose Up / -- +goose
// Down markers.
func splitGooseSections(data string) (up, down string, err error) {
	var upB, downB strings.Builder
	current := (*strings.Builder)(nil)
	for _, line := range strings.Split(data, "\n") {
		if marker := gooseMarker(line); marker != "" {
			switch marker {
			case "Up":
				current = &upB
			case "Down":
				current = &downB
			default:
				return "", "", fmt.Errorf("unknown +goose marker %q", marker)
			}
			continue
		}
		if current != nil {
			current.WriteString(line)
			current.WriteString("\n")
		}
	}
	if upB.Len() == 0 {
		return "", "", fmt.Errorf("missing -- +goose Up section")
	}
	if downB.Len() == 0 {
		return "", "", fmt.Errorf("missing -- +goose Down section (migrations must be reversible)")
	}
	return upB.String(), downB.String(), nil
}

// gooseMarker returns "Up"/"Down" when line is a goose section marker.
func gooseMarker(line string) string {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "--") {
		return ""
	}
	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "--"))
	if !strings.HasPrefix(rest, "+goose") {
		return ""
	}
	fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(rest, "+goose")))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// splitStatements splits SQL on semicolons, ignoring semicolons inside
// single/double-quoted strings, dollar-quoted blocks, and comments.
func splitStatements(sql string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	i, n := 0, len(sql)
	for i < n {
		c := sql[i]
		switch {
		case c == '\'':
			j := i + 1
			for j < n {
				if sql[j] == '\'' {
					if j+1 < n && sql[j+1] == '\'' {
						j += 2 // escaped quote
						continue
					}
					j++
					break
				}
				j++
			}
			cur.WriteString(sql[i:j])
			i = j
		case c == '"':
			j := i + 1
			for j < n {
				if sql[j] == '"' {
					if j+1 < n && sql[j+1] == '"' {
						j += 2
						continue
					}
					j++
					break
				}
				j++
			}
			cur.WriteString(sql[i:j])
			i = j
		case c == '$' && isDollarTagStart(sql, i):
			tag := dollarTag(sql, i)
			end := strings.Index(sql[i+len(tag):], tag)
			if end < 0 {
				cur.WriteString(sql[i:])
				i = n
			} else {
				j := i + len(tag) + end + len(tag)
				cur.WriteString(sql[i:j])
				i = j
			}
		case c == '-' && i+1 < n && sql[i+1] == '-':
			// Comments collapse to a space (never copied): this keeps
			// tokens separated while comment-only fragments flush empty.
			cur.WriteByte(' ')
			j := strings.IndexByte(sql[i:], '\n')
			if j < 0 {
				i = n
			} else {
				i += j
			}
		case c == '/' && i+1 < n && sql[i+1] == '*':
			cur.WriteByte(' ')
			end := strings.Index(sql[i+2:], "*/")
			if end < 0 {
				i = n
			} else {
				i += 2 + end + 2
			}
		case c == ';':
			cur.WriteByte(c)
			flush()
			i++
		default:
			cur.WriteByte(c)
			i++
		}
	}
	flush()
	return out
}

// isDollarTagStart reports whether sql[i:] opens a dollar-quoted string.
func isDollarTagStart(sql string, i int) bool {
	j := i + 1
	for j < len(sql) && sql[j] != '$' {
		c := sql[j]
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
		j++
	}
	return j < len(sql)
}

// dollarTag returns the opening tag ($$ or $name$) at sql[i].
func dollarTag(sql string, i int) string {
	j := i + 1
	for j < len(sql) && sql[j] != '$' {
		j++
	}
	return sql[i : j+1]
}

func (r Runner) ensureVersionTable(ctx context.Context, db DB) error {
	// Exact goose v3 postgres shape.
	return db.Exec(ctx, `CREATE TABLE IF NOT EXISTS goose_db_version (
		id integer PRIMARY KEY GENERATED BY DEFAULT AS IDENTITY,
		version_id bigint NOT NULL,
		is_applied boolean NOT NULL,
		tstamp timestamp NOT NULL DEFAULT now()
	)`)
}

// appliedVersions returns the set of applied versions (latest row per
// version decides, mirroring goose).
func (r Runner) appliedVersions(ctx context.Context, db DB) (map[int64]bool, error) {
	rows, err := db.Query(ctx, `SELECT version_id, is_applied FROM goose_db_version ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	applied := map[int64]bool{}
	seen := map[int64]bool{}
	for rows.Next() {
		var version int64
		var isApplied bool
		if err := rows.Scan(&version, &isApplied); err != nil {
			return nil, err
		}
		if seen[version] {
			continue
		}
		seen[version] = true
		applied[version] = isApplied
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return applied, nil
}

// Up applies every pending migration in version order. Each migration runs
// in its own transaction (statements + version row commit atomically).
func (r Runner) Up(ctx context.Context, db DB, dir string) ([]Migration, error) {
	migrations, err := r.Collect(dir)
	if err != nil {
		return nil, err
	}
	if err := r.ensureVersionTable(ctx, db); err != nil {
		return nil, fmt.Errorf("ensure version table: %w", err)
	}
	applied, err := r.appliedVersions(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("list applied versions: %w", err)
	}
	var done []Migration
	for _, m := range migrations {
		if applied[m.Version] {
			continue
		}
		if err := r.applyUp(ctx, db, m); err != nil {
			return done, err
		}
		done = append(done, m)
		r.log().Info("migration applied", "version", m.Version, "name", m.Name)
	}
	return done, nil
}

func (r Runner) applyUp(ctx context.Context, db DB, m Migration) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migration %d: begin: %w", m.Version, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, stmt := range m.Up {
		if err := tx.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("migration %d up: %w", m.Version, err)
		}
	}
	if err := tx.Exec(ctx, `INSERT INTO goose_db_version (version_id, is_applied) VALUES ($1, $2)`, m.Version, true); err != nil {
		return fmt.Errorf("migration %d: record version: %w", m.Version, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migration %d: commit: %w", m.Version, err)
	}
	return nil
}

// Down reverts the latest applied migration.
func (r Runner) Down(ctx context.Context, db DB, dir string) (Migration, error) {
	migrations, err := r.Collect(dir)
	if err != nil {
		return Migration{}, err
	}
	if err := r.ensureVersionTable(ctx, db); err != nil {
		return Migration{}, fmt.Errorf("ensure version table: %w", err)
	}
	applied, err := r.appliedVersions(ctx, db)
	if err != nil {
		return Migration{}, fmt.Errorf("list applied versions: %w", err)
	}
	for i := len(migrations) - 1; i >= 0; i-- {
		m := migrations[i]
		if !applied[m.Version] {
			continue
		}
		tx, err := db.Begin(ctx)
		if err != nil {
			return Migration{}, fmt.Errorf("migration %d: begin: %w", m.Version, err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		for _, stmt := range m.Down {
			if err := tx.Exec(ctx, stmt); err != nil {
				return Migration{}, fmt.Errorf("migration %d down: %w", m.Version, err)
			}
		}
		if err := tx.Exec(ctx, `DELETE FROM goose_db_version WHERE version_id=$1`, m.Version); err != nil {
			return Migration{}, fmt.Errorf("migration %d: delete version: %w", m.Version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return Migration{}, fmt.Errorf("migration %d: commit: %w", m.Version, err)
		}
		r.log().Info("migration reverted", "version", m.Version, "name", m.Name)
		return m, nil
	}
	return Migration{}, fmt.Errorf("no applied migration to revert")
}

// MigrationStatus is one row of `migrate status`.
type MigrationStatus struct {
	Version int64
	Name    string
	Applied bool
}

// Status lists every known migration with its applied state.
func (r Runner) Status(ctx context.Context, db DB, dir string) ([]MigrationStatus, error) {
	migrations, err := r.Collect(dir)
	if err != nil {
		return nil, err
	}
	if err := r.ensureVersionTable(ctx, db); err != nil {
		return nil, fmt.Errorf("ensure version table: %w", err)
	}
	applied, err := r.appliedVersions(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("list applied versions: %w", err)
	}
	out := make([]MigrationStatus, 0, len(migrations))
	for _, m := range migrations {
		out = append(out, MigrationStatus{Version: m.Version, Name: m.Name, Applied: applied[m.Version]})
	}
	return out, nil
}

func newMigrateCommand(configPath *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Manage database migrations (up, down, status)",
	}
	registerFlags(cmd, true)
	cmd.AddCommand(
		&cobra.Command{
			Use:   "up",
			Short: "Apply all pending migrations",
			RunE: func(cmd *cobra.Command, _ []string) error {
				cfg, db, err := openMigrationDB(cmd, *configPath)
				if err != nil {
					return err
				}
				defer db.pool.Close()
				applied, err := Runner{}.Up(cmd.Context(), pgxDB{pool: db.pool}, cfg.MigrationsDir)
				if err != nil {
					return err
				}
				if len(applied) == 0 {
					cmd.Println("already up to date")
					return nil
				}
				for _, m := range applied {
					cmd.Printf("applied %05d %s\n", m.Version, m.Name)
				}
				return nil
			},
		},
		&cobra.Command{
			Use:   "down",
			Short: "Revert the latest applied migration",
			RunE: func(cmd *cobra.Command, _ []string) error {
				cfg, db, err := openMigrationDB(cmd, *configPath)
				if err != nil {
					return err
				}
				defer db.pool.Close()
				m, err := Runner{}.Down(cmd.Context(), pgxDB{pool: db.pool}, cfg.MigrationsDir)
				if err != nil {
					return err
				}
				cmd.Printf("reverted %05d %s\n", m.Version, m.Name)
				return nil
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "List migrations with applied state",
			RunE: func(cmd *cobra.Command, _ []string) error {
				cfg, db, err := openMigrationDB(cmd, *configPath)
				if err != nil {
					return err
				}
				defer db.pool.Close()
				rows, err := Runner{}.Status(cmd.Context(), pgxDB{pool: db.pool}, cfg.MigrationsDir)
				if err != nil {
					return err
				}
				for _, s := range rows {
					state := "pending"
					if s.Applied {
						state = "applied"
					}
					cmd.Printf("%-8s %05d %s\n", state, s.Version, s.Name)
				}
				return nil
			},
		},
	)
	return cmd
}

type migrationDB struct {
	pool *pgxpool.Pool
}

func openMigrationDB(cmd *cobra.Command, configPath string) (Config, *migrationDB, error) {
	bindRunFlags(cmd)
	cfg, err := loadConfig(configPath)
	if err != nil {
		return Config{}, nil, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, nil, err
	}
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		return Config{}, nil, fmt.Errorf("connect database: %w", err)
	}
	return cfg, &migrationDB{pool: pool}, nil
}
