package agent

import (
	"bytes"
	"context"
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
