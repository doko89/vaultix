package agent

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeBin installs an executable stub script on PATH and returns a cleanup
// that restores PATH. The stub records its argv to argvFile.
func fakeBin(t *testing.T, name, body string) (argvFile string) {
	t.Helper()
	dir := t.TempDir()
	argvFile = filepath.Join(dir, name+".argv")
	script := "#!/bin/sh\necho \"$@\" > " + argvFile + "\n" + body
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(script), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return argvFile
}

func readArgv(t *testing.T, argvFile string) string {
	t.Helper()
	raw, err := os.ReadFile(argvFile)
	require.NoError(t, err)
	return strings.TrimSpace(string(raw))
}

func TestDumpPostgresArgsAndOutput(t *testing.T) {
	argv := fakeBin(t, "pg_dump", "printf 'PGDUMP-BYTES'\n")
	var out bytes.Buffer
	n, err := DumpPostgres(context.Background(), &out, "job-1", "appdb",
		DumpOptions{Host: "db.local", Port: 5433, User: "vaultix"}, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(len("PGDUMP-BYTES")), n)
	assert.Equal(t, "PGDUMP-BYTES", out.String())
	got := readArgv(t, argv)
	assert.Contains(t, got, "--format=custom", "must use custom format")
	assert.Contains(t, got, "--host=db.local")
	assert.Contains(t, got, "--port=5433")
	assert.Contains(t, got, "--username=vaultix")
	assert.Contains(t, got, "appdb")
	assert.NotContains(t, got, "s3cr3t", "argv must never carry secrets")
}

func TestDumpPostgresStderrCapture(t *testing.T) {
	fakeBin(t, "pg_dump", "echo 'pg_dump: error: connection refused' >&2\nexit 1\n")
	var out bytes.Buffer
	_, err := DumpPostgres(context.Background(), &out, "job-9", "appdb", DumpOptions{}, nil)
	require.Error(t, err)
	var execErr *ExecError
	require.ErrorAs(t, err, &execErr)
	assert.Equal(t, "job-9", execErr.JobID)
	assert.Equal(t, "pg_dump", execErr.Stage)
	assert.Equal(t, 1, execErr.ExitCode)
	assert.Contains(t, execErr.Stderr, "connection refused")
	assert.Contains(t, err.Error(), "job-9")
}

func TestDumpPostgresTimeout(t *testing.T) {
	fakeBin(t, "pg_dump", "sleep 30\n")
	var out bytes.Buffer
	ctx := context.Background()
	_, err := DumpPostgres(ctx, &out, "job-1", "appdb", DumpOptions{Timeout: 100 * time.Millisecond}, nil)
	require.Error(t, err)
	var execErr *ExecError
	require.ErrorAs(t, err, &execErr)
	assert.True(t, strings.Contains(execErr.Cause, "killed") || strings.Contains(execErr.Cause, "deadline"),
		"cause should reflect the timeout, got %q", execErr.Cause)
}

func TestDumpPostgresMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty PATH: no pg_dump
	var out bytes.Buffer
	_, err := DumpPostgres(context.Background(), &out, "job-1", "appdb", DumpOptions{}, nil)
	require.ErrorContains(t, err, "pg_dump not found")
}

func TestDumpPostgresNeedsDatabase(t *testing.T) {
	var out bytes.Buffer
	_, err := DumpPostgres(context.Background(), &out, "job-1", "", DumpOptions{}, nil)
	require.ErrorContains(t, err, "no database selected")
}

func TestDumpMySQLArgsAndOutput(t *testing.T) {
	argv := fakeBin(t, "mysqldump", "printf 'MYSQLDUMP-BYTES'\n")
	var out bytes.Buffer
	n, err := DumpMySQL(context.Background(), &out, "job-2", "shopdb",
		DumpOptions{Host: "mysql.local", User: "backup", Password: "s3cr3t"}, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(len("MYSQLDUMP-BYTES")), n)
	got := readArgv(t, argv)
	assert.Contains(t, got, "--single-transaction")
	assert.Contains(t, got, "--host=mysql.local")
	assert.Contains(t, got, "--user=backup")
	assert.Contains(t, got, "shopdb")
	assert.NotContains(t, got, "s3cr3t", "password must not appear in argv")
}

func TestDumpMySQLStderrCapture(t *testing.T) {
	fakeBin(t, "mysqldump", "echo 'mysqldump: Got error: 1045: Access denied' >&2\nexit 2\n")
	var out bytes.Buffer
	_, err := DumpMySQL(context.Background(), &out, "job-3", "shopdb", DumpOptions{}, nil)
	require.Error(t, err)
	var execErr *ExecError
	require.ErrorAs(t, err, &execErr)
	assert.Equal(t, 2, execErr.ExitCode)
	assert.Contains(t, execErr.Stderr, "Access denied")
}

func TestParseDumpOptions(t *testing.T) {
	o, err := ParseDumpOptions(map[string]any{
		"host": "h", "port": float64(5432), "database": "d",
		"user": "u", "password": "p", "timeout_seconds": float64(60),
	})
	require.NoError(t, err)
	assert.Equal(t, "h", o.Host)
	assert.Equal(t, 5432, o.Port)
	assert.Equal(t, "d", o.Database)
	assert.Equal(t, time.Minute, o.Timeout)

	o, err = ParseDumpOptions(nil)
	require.NoError(t, err)
	assert.Equal(t, DefaultDumpTimeout, o.Timeout)
}

// ---------- issued db_credential (F2) ----------

const testIssuedPassword = "issued-pw-9xQ!"

// captureSlog swaps the default logger for a capturing handler. No test in
// this package runs parallel, so the process-global swap is safe.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestParseDumpOptionsDBCredentialStringForm(t *testing.T) {
	o, err := ParseDumpOptions(map[string]any{
		"database":      "appdb",
		"db_credential": `{"username":"vaultix","password":"` + testIssuedPassword + `","host":"db.internal","port":5433}`,
	})
	require.NoError(t, err)
	assert.Equal(t, "vaultix", o.User)
	assert.Equal(t, testIssuedPassword, o.Password)
	assert.Equal(t, "db.internal", o.Host)
	assert.Equal(t, 5433, o.Port)
	assert.Equal(t, "appdb", o.Database)
	assert.True(t, o.CredentialIssued)
}

func TestParseDumpOptionsDBCredentialObjectForm(t *testing.T) {
	o, err := ParseDumpOptions(map[string]any{
		"db_credential": map[string]any{
			"username": "backup", "password": testIssuedPassword,
			"host": "mysql.internal", "port": "3306",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "backup", o.User)
	assert.Equal(t, testIssuedPassword, o.Password)
	assert.Equal(t, "mysql.internal", o.Host)
	assert.Equal(t, 3306, o.Port)
	assert.True(t, o.CredentialIssued)
}

func TestParseDumpOptionsDBCredentialPrecedence(t *testing.T) {
	// Identity always comes from the issued credential; flat host/port
	// (policy endpoint config) win when set, else the credential fills in.
	o, err := ParseDumpOptions(map[string]any{
		"host": "flat.local", "port": 1111, "user": "flatuser", "password": "flatpw",
		"db_credential": map[string]any{
			"username": "issued", "password": testIssuedPassword,
			"host": "cred.local", "port": 5432,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "issued", o.User, "issued username wins over flat user")
	assert.Equal(t, testIssuedPassword, o.Password, "issued password wins over flat password")
	assert.Equal(t, "flat.local", o.Host, "flat host wins when set")
	assert.Equal(t, 1111, o.Port, "flat port wins when set")
	assert.True(t, o.CredentialIssued)

	o, err = ParseDumpOptions(map[string]any{
		"db_credential": map[string]any{
			"username": "issued", "password": testIssuedPassword,
			"host": "cred.local", "port": 5432,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "cred.local", o.Host, "credential fills unset host")
	assert.Equal(t, 5432, o.Port, "credential fills unset port")
}

func TestParseDumpOptionsDBCredentialInvalid(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"bad JSON", `{"username": oops`, "not a valid credential object"},
		{"empty string", "  ", "must not be empty"},
		{"wrong type", 42, "must be an object"},
		{"missing username", map[string]any{"password": testIssuedPassword}, `"username"`},
		{"missing password", map[string]any{"username": "u"}, `"password"`},
		{"empty password", map[string]any{"username": "u", "password": ""}, `"password"`},
		{"non-string password", map[string]any{"username": "u", "password": 42}, "must be a string"},
		{"port out of range", map[string]any{"username": "u", "password": "p", "port": 99999}, "port"},
		{"port garbage", map[string]any{"username": "u", "password": "p", "port": "abc"}, "must be a number"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseDumpOptions(map[string]any{"db_credential": tc.value})
			require.ErrorContains(t, err, tc.want)
			assert.NotContains(t, err.Error(), testIssuedPassword,
				"parse errors must never echo credential material")
		})
	}

	// Absent credential parses cleanly and leaves the fallback flag off.
	o, err := ParseDumpOptions(map[string]any{"host": "h"})
	require.NoError(t, err)
	assert.False(t, o.CredentialIssued)
}

// envAssertStub installs a stub that fails unless wantVar carries wantVal,
// proving env delivery. It records argv for the absence assertion.
func envAssertStub(t *testing.T, name, wantVar, wantVal string) (argvFile string) {
	t.Helper()
	body := "if [ \"$" + wantVar + "\" != '" + wantVal + "' ]; then " +
		"echo 'expected " + wantVar + " in environment' >&2; exit 3; fi\n" +
		"printf 'DUMP-BYTES'\n"
	return fakeBin(t, name, body)
}

func TestDumpPostgresIssuedCredentialEnv(t *testing.T) {
	argv := envAssertStub(t, "pg_dump", "PGPASSWORD", testIssuedPassword)
	o, err := ParseDumpOptions(map[string]any{
		"database":      "appdb",
		"db_credential": `{"username":"vaultix","password":"` + testIssuedPassword + `"}`,
	})
	require.NoError(t, err)
	require.True(t, o.CredentialIssued)

	var out bytes.Buffer
	n, err := DumpPostgres(context.Background(), &out, "job-1", "", o, nil)
	require.NoError(t, err, "stub exits 3 unless PGPASSWORD arrived via env")
	assert.Equal(t, int64(len("DUMP-BYTES")), n)
	got := readArgv(t, argv)
	assert.Contains(t, got, "--username=vaultix")
	assert.NotContains(t, got, testIssuedPassword, "password must never appear in argv")
	assert.NotContains(t, got, "PGPASSWORD", "env transport must not leak into argv")
}

func TestDumpMySQLIssuedCredentialEnv(t *testing.T) {
	argv := envAssertStub(t, "mysqldump", "MYSQL_PWD", testIssuedPassword)
	o, err := ParseDumpOptions(map[string]any{
		"database": "shopdb",
		"db_credential": map[string]any{
			"username": "backup", "password": testIssuedPassword,
		},
	})
	require.NoError(t, err)

	var out bytes.Buffer
	_, err = DumpMySQL(context.Background(), &out, "job-2", "", o, nil)
	require.NoError(t, err, "stub exits 3 unless MYSQL_PWD arrived via env")
	got := readArgv(t, argv)
	assert.Contains(t, got, "--user=backup")
	assert.NotContains(t, got, testIssuedPassword, "password must never appear in argv")
	assert.NotContains(t, got, "MYSQL_PWD", "env transport must not leak into argv")
}

func TestDumpPasswordRedactedInErrors(t *testing.T) {
	const pw = "leakM3-hunter2"
	dumpers := map[string]struct {
		bin string
		run func(ctx context.Context, w *bytes.Buffer, opts DumpOptions) error
	}{
		"postgres": {"pg_dump", func(ctx context.Context, w *bytes.Buffer, opts DumpOptions) error {
			_, err := DumpPostgres(ctx, w, "job-9", "appdb", opts, nil)
			return err
		}},
		"mysql": {"mysqldump", func(ctx context.Context, w *bytes.Buffer, opts DumpOptions) error {
			_, err := DumpMySQL(ctx, w, "job-9", "shopdb", opts, nil)
			return err
		}},
	}
	for name, d := range dumpers {
		t.Run(name+"/stderr echo", func(t *testing.T) {
			// A hostile/chatty tool echoing the password must not leak it
			// into the structured error.
			fakeBin(t, d.bin, "echo \"auth failed for password '"+pw+"'\" >&2\nexit 2\n")
			var out bytes.Buffer
			err := d.run(context.Background(), &out,
				DumpOptions{Password: pw, CredentialIssued: true})
			require.Error(t, err)
			var execErr *ExecError
			require.ErrorAs(t, err, &execErr)
			assert.Contains(t, execErr.Stderr, "[REDACTED]")
			assert.NotContains(t, execErr.Stderr, pw)
			assert.NotContains(t, execErr.Cause, pw)
			assert.NotContains(t, err.Error(), pw)
		})
		t.Run(name+"/no database", func(t *testing.T) {
			// Empty selector and no options.database fails before any
			// binary lookup, so no stub is installed here.
			var out bytes.Buffer
			var err error
			if name == "postgres" {
				_, err = DumpPostgres(context.Background(), &out, "job-9", "",
					DumpOptions{Password: pw, CredentialIssued: true}, nil)
			} else {
				_, err = DumpMySQL(context.Background(), &out, "job-9", "",
					DumpOptions{Password: pw, CredentialIssued: true}, nil)
			}
			require.ErrorContains(t, err, "no database selected")
			assert.NotContains(t, err.Error(), pw)
		})
		t.Run(name+"/missing binary", func(t *testing.T) {
			t.Setenv("PATH", t.TempDir())
			var out bytes.Buffer
			err := d.run(context.Background(), &out,
				DumpOptions{Password: pw, CredentialIssued: true})
			require.Error(t, err)
			assert.NotContains(t, err.Error(), pw)
		})
	}
}

func TestDumpNoCredentialWarnsAndFallsBack(t *testing.T) {
	logs := captureSlog(t)
	argv := fakeBin(t, "pg_dump", "printf 'PGDUMP-BYTES'\n")
	var out bytes.Buffer
	// No issued credential, but a flat (legacy) password: the dump still
	// runs via env, with a warning that nothing was issued.
	n, err := DumpPostgres(context.Background(), &out, "job-legacy", "appdb",
		DumpOptions{User: "vaultix", Password: "w4rn-flat-pw"}, nil)
	require.NoError(t, err, "fallback must keep working against old managers")
	assert.Equal(t, int64(len("PGDUMP-BYTES")), n)
	assert.Contains(t, logs.String(), "no db_credential issued")
	assert.Contains(t, logs.String(), "job-legacy")
	assert.NotContains(t, logs.String(), "w4rn-flat-pw", "warning must not log credential material")
	assert.NotContains(t, readArgv(t, argv), "w4rn-flat-pw")
}

func TestDumpIssuedCredentialStaysSilent(t *testing.T) {
	logs := captureSlog(t)
	fakeBin(t, "mysqldump", "printf 'MYSQLDUMP-BYTES'\n")
	o, err := ParseDumpOptions(map[string]any{
		"db_credential": `{"username":"backup","password":"` + testIssuedPassword + `"}`,
	})
	require.NoError(t, err)
	var out bytes.Buffer
	_, err = DumpMySQL(context.Background(), &out, "job-1", "shopdb", o, nil)
	require.NoError(t, err)
	assert.NotContains(t, logs.String(), "no db_credential issued")
	assert.NotContains(t, logs.String(), testIssuedPassword)
}

func TestRedactSecret(t *testing.T) {
	assert.Equal(t, "a [REDACTED] b", redactSecret("a pw b", "pw"))
	assert.Equal(t, "pw pw", redactSecret("pw pw", ""), "empty secret scrubs nothing")
	assert.Equal(t, "clean", redactSecret("clean", "pw"))
}

func TestOptIntVariants(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want int
	}{
		{int(7), 7},
		{int64(8), 8},
		{float64(9), 9},
		{"10", 10},
		{" 11 ", 11},
	} {
		got, err := optInt(map[string]any{"k": tc.in}, "k")
		require.NoError(t, err)
		assert.Equal(t, tc.want, got)
	}
	_, err := optInt(map[string]any{}, "k")
	require.NoError(t, err)
	_, err = optInt(map[string]any{"k": "abc"}, "k")
	require.ErrorContains(t, err, "must be a number")
	_, err = optInt(map[string]any{"k": true}, "k")
	require.ErrorContains(t, err, "must be a number")
	_, err = optString(map[string]any{"k": 42}, "k")
	require.ErrorContains(t, err, "must be a string")
}
