package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSQLCmd installs a sqlcmd stub on PATH and returns its argv recorder.
// The stub answers the `-?` version probe and, for backup runs, writes fake
// bytes to the TO DISK path parsed out of the -Q argument. Behavior fixtures
// ride on SQLCMD_STUB_* env vars (set via env, restored after the test):
//
//	SQLCMD_STUB_WANT_PW  fail (exit 3) unless $SQLCMDPASSWORD equals it
//	SQLCMD_STUB_EXIT     exit code for backup runs (default 0)
//	SQLCMD_STUB_STDERR   line printed to stderr on backup runs
//	SQLCMD_STUB_SKIP_BAK "1" = exit 0 without writing the .bak
//	SQLCMD_STUB_EMPTY_BAK "1" = write a 0-byte .bak
//	SQLCMD_STUB_SLEEP    seconds to sleep before acting (timeout tests)
//	SQLCMD_STUB_PAYLOAD  .bak bytes (default MSSQLBAK-BYTES)
func fakeSQLCmd(t *testing.T, env map[string]string) (argvFile string) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
	body := `if [ "$1" = "-?" ]; then
echo "Microsoft (R) SQL Server Command Line Tool Version 18.4.0001.0"
exit 0
fi
if [ -n "$SQLCMD_STUB_SLEEP" ]; then sleep "$SQLCMD_STUB_SLEEP"; fi
if [ -n "$SQLCMD_STUB_WANT_PW" ]; then
if [ "$SQLCMDPASSWORD" != "$SQLCMD_STUB_WANT_PW" ]; then
echo "expected SQLCMDPASSWORD in environment" >&2; exit 3; fi
fi
if [ -n "$SQLCMD_STUB_STDERR" ]; then echo "$SQLCMD_STUB_STDERR" >&2; fi
if [ "$SQLCMD_STUB_SKIP_BAK" != "1" ]; then
q=""; prev=""
for a in "$@"; do if [ "$prev" = "-Q" ]; then q="$a"; fi; prev="$a"; done
p=$(printf '%s' "$q" | sed -n "s/.*TO DISK = N'\(.*\)' WITH.*/\1/p")
if [ -n "$p" ]; then
if [ "$SQLCMD_STUB_EMPTY_BAK" = "1" ]; then : > "$p"; else printf '%s' "${SQLCMD_STUB_PAYLOAD:-MSSQLBAK-BYTES}" > "$p"; fi
fi
fi
exit "${SQLCMD_STUB_EXIT:-0}"
`
	return fakeBin(t, "sqlcmd", body)
}

func TestProbeSQLCmd(t *testing.T) {
	argv := fakeSQLCmd(t, nil)
	assert.True(t, ProbeSQLCmd(nil), "stub answering -? must probe present")
	assert.Contains(t, readArgv(t, argv), "-?", "probe must run `sqlcmd -?`")

	t.Setenv("PATH", t.TempDir()) // empty PATH: no sqlcmd
	assert.False(t, ProbeSQLCmd(nil), "missing binary must probe absent")
}

func TestProbeSQLCmdFailingBinary(t *testing.T) {
	fakeBin(t, "sqlcmd", "exit 1\n")
	assert.False(t, ProbeSQLCmd(nil), "binary failing -? must probe absent")
}

func TestDetectCapabilitiesMatrix(t *testing.T) {
	assert.Nil(t, detectCapabilities(false, false))
	assert.Equal(t, []string{"docker"}, detectCapabilities(true, false))
	assert.Equal(t, []string{"mssql"}, detectCapabilities(false, true))
	assert.Equal(t, []string{"docker", "mssql"}, detectCapabilities(true, true))
}

func TestHeartbeatSendsCapabilities(t *testing.T) {
	stub := &StubManagerClient{}
	r := &HeartbeatRunner{Client: stub, Config: testConfig(), Capabilities: []string{"mssql"}}
	_, err := r.Beat(context.Background())
	require.NoError(t, err)
	require.Len(t, stub.Heartbeats, 1)
	assert.Equal(t, []string{"mssql"}, stub.Heartbeats[0].Capabilities)
}

func TestHeartbeatLeavesCapabilitiesNilByDefault(t *testing.T) {
	stub := &StubManagerClient{}
	r := &HeartbeatRunner{Client: stub, Config: testConfig()}
	_, err := r.Beat(context.Background())
	require.NoError(t, err)
	require.Len(t, stub.Heartbeats, 1)
	assert.Nil(t, stub.Heartbeats[0].Capabilities, "unset capabilities must stay nil so the manager keeps its stored set")
}

func TestMSSQLBackupTSQLExact(t *testing.T) {
	assert.Equal(t,
		`BACKUP DATABASE [appdb] TO DISK = N'/var/back/t0-appdb.bak' WITH COMPRESSION, CHECKSUM, INIT, STATS = 10`,
		MSSQLBackupTSQL("appdb", "/var/back/t0-appdb.bak"))
}

func TestMSSQLBackupTSQLEscapes(t *testing.T) {
	got := MSSQLBackupTSQL("a]b", `C:\back\o'b.bak`)
	assert.Equal(t,
		`BACKUP DATABASE [a]]b] TO DISK = N'C:\back\o''b.bak' WITH COMPRESSION, CHECKSUM, INIT, STATS = 10`,
		got)
}

func TestValidateMSSQLIdentifier(t *testing.T) {
	valid := []string{"appdb", "my-db_2.0", "DB01", "a]b", "#tmp", "Master"}
	for _, db := range valid {
		assert.NoError(t, ValidateMSSQLIdentifier(db), "want valid: %q", db)
	}
	invalid := map[string]string{
		"":                    "no database selected",
		"a;b":                 "plain identifier",
		"a--b":                "plain identifier",
		"--x":                 "plain identifier",
		"a'b":                 "plain identifier",
		`a"b`:                 "plain identifier",
		"a`b":                 "plain identifier",
		"a b":                 "plain identifier",
		"a\tb":                "plain identifier",
		"a\nb":                "plain identifier",
		" a":                  "plain identifier",
		"a/b":                 "not a path",
		`a\b`:                 "not a path",
		"a\x00b":              "not a path",
		"a\x01b":              "plain identifier",
		"a\x7fb":              "plain identifier",
		"[a]; DROP TABLE t--": "plain identifier",
	}
	for db, want := range invalid {
		err := ValidateMSSQLIdentifier(db)
		require.Error(t, err, "want invalid: %q", db)
		assert.Contains(t, err.Error(), want, "db %q", db)
	}
}

func TestMSSQLServerRendering(t *testing.T) {
	assert.Equal(t, "localhost", mssqlServer("", 0))
	assert.Equal(t, "db.local", mssqlServer("db.local", 0))
	assert.Equal(t, "db.local", mssqlServer("db.local", 1433), "default port stays implicit")
	assert.Equal(t, "db.local,1444", mssqlServer("db.local", 1444))
	assert.Equal(t, "localhost,1444", mssqlServer("", 1444))
}

func TestBackupMSSQLArgsAndPayload(t *testing.T) {
	argv := fakeSQLCmd(t, nil)
	staging := t.TempDir()
	var out bytes.Buffer
	n, err := BackupMSSQL(context.Background(), &out, "job-1", 0, "appdb",
		MSSQLOptions{Host: "db.local", Port: 1444, User: "sa", Password: "s3cr3t", StagingDir: staging, CredentialIssued: true},
		t.TempDir(), nil)
	require.NoError(t, err)
	assert.Equal(t, int64(len("MSSQLBAK-BYTES")), n)
	assert.Equal(t, "MSSQLBAK-BYTES", out.String())

	got := readArgv(t, argv)
	assert.Contains(t, got, "-b", "sqlcmd must exit nonzero on T-SQL errors")
	assert.Contains(t, got, "-S db.local,1444")
	assert.Contains(t, got, "-U sa")
	assert.Contains(t, got, "BACKUP DATABASE [appdb] TO DISK = N'")
	assert.Contains(t, got, "WITH COMPRESSION, CHECKSUM, INIT, STATS = 10")
	assert.NotContains(t, got, "-C", "trust_server_cert defaults to false")
	assert.NotContains(t, got, "-P", "password must never use the -P flag")
	assert.NotContains(t, got, "s3cr3t", "password must never appear in argv")
	assert.NotContains(t, got, "SQLCMDPASSWORD", "env transport must not leak into argv")

	entries, err := os.ReadDir(staging)
	require.NoError(t, err)
	assert.Empty(t, entries, "staging .bak must be deleted on success")
}

func TestBackupMSSQLPasswordViaEnv(t *testing.T) {
	argv := fakeSQLCmd(t, map[string]string{"SQLCMD_STUB_WANT_PW": testIssuedPassword})
	o, err := ParseMSSQLOptions(map[string]any{
		"database":      "appdb",
		"db_credential": `{"username":"sa","password":"` + testIssuedPassword + `","host":"db.internal","port":1433}`,
	})
	require.NoError(t, err)
	require.True(t, o.CredentialIssued)

	var out bytes.Buffer
	_, err = BackupMSSQL(context.Background(), &out, "job-1", 0, "", o, t.TempDir(), nil)
	require.NoError(t, err, "stub exits 3 unless SQLCMDPASSWORD arrived via env")
	got := readArgv(t, argv)
	assert.Contains(t, got, "-U sa")
	assert.Contains(t, got, "-S db.internal")
	assert.NotContains(t, got, testIssuedPassword)
}

func TestBackupMSSQLTrustServerCert(t *testing.T) {
	argv := fakeSQLCmd(t, nil)
	var out bytes.Buffer
	_, err := BackupMSSQL(context.Background(), &out, "job-1", 0, "appdb",
		MSSQLOptions{TrustServerCert: true, CredentialIssued: true}, t.TempDir(), nil)
	require.NoError(t, err)
	assert.Contains(t, readArgv(t, argv), "-C")
}

func TestBackupMSSQLMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var out bytes.Buffer
	_, err := BackupMSSQL(context.Background(), &out, "job-1", 0, "appdb",
		MSSQLOptions{Password: "pw"}, t.TempDir(), nil)
	require.ErrorContains(t, err, "sqlcmd not found on PATH")
	require.ErrorContains(t, err, "mssql-tools")
	assert.NotContains(t, err.Error(), "pw")
}

func TestBackupMSSQLTimeout(t *testing.T) {
	fakeSQLCmd(t, map[string]string{"SQLCMD_STUB_SLEEP": "30"})
	var out bytes.Buffer
	_, err := BackupMSSQL(context.Background(), &out, "job-1", 0, "appdb",
		MSSQLOptions{Timeout: 100 * time.Millisecond, CredentialIssued: true}, t.TempDir(), nil)
	require.Error(t, err)
	var execErr *ExecError
	require.ErrorAs(t, err, &execErr)
	assert.True(t, strings.Contains(execErr.Cause, "killed") || strings.Contains(execErr.Cause, "deadline"),
		"cause should reflect the timeout, got %q", execErr.Cause)
}

func TestBackupMSSQLNeedsDatabase(t *testing.T) {
	var out bytes.Buffer
	_, err := BackupMSSQL(context.Background(), &out, "job-1", 0, "",
		MSSQLOptions{Password: testIssuedPassword, CredentialIssued: true}, t.TempDir(), nil)
	require.ErrorContains(t, err, "no database selected")
	assert.NotContains(t, err.Error(), testIssuedPassword)
}

func TestBackupMSSQLRejectsInjection(t *testing.T) {
	argv := fakeSQLCmd(t, nil)
	var out bytes.Buffer
	_, err := BackupMSSQL(context.Background(), &out, "job-1", 0, "appdb; DROP TABLE t--",
		MSSQLOptions{CredentialIssued: true}, t.TempDir(), nil)
	require.ErrorContains(t, err, "plain identifier")
	_, statErr := os.Stat(argv)
	assert.True(t, os.IsNotExist(statErr), "rejected input must never reach sqlcmd")
}

func TestBackupMSSQLStderrCaptureAndRedaction(t *testing.T) {
	const pw = "leakM3-hunter2"
	fakeSQLCmd(t, map[string]string{
		"SQLCMD_STUB_EXIT":   "1",
		"SQLCMD_STUB_STDERR": "Sqlcmd: Login failed for user 'sa' with password '" + pw + "'",
	})
	var out bytes.Buffer
	_, err := BackupMSSQL(context.Background(), &out, "job-9", 0, "appdb",
		MSSQLOptions{Password: pw, CredentialIssued: true}, t.TempDir(), nil)
	require.Error(t, err)
	var execErr *ExecError
	require.ErrorAs(t, err, &execErr)
	assert.Equal(t, "job-9", execErr.JobID)
	assert.Equal(t, "sqlcmd", execErr.Stage)
	assert.Equal(t, 1, execErr.ExitCode)
	assert.Contains(t, execErr.Stderr, "[REDACTED]")
	assert.NotContains(t, execErr.Stderr, pw)
	assert.NotContains(t, execErr.Cause, pw)
	assert.NotContains(t, err.Error(), pw)
}

func TestBackupMSSQLStagingMissingFailsActionably(t *testing.T) {
	fakeSQLCmd(t, map[string]string{"SQLCMD_STUB_SKIP_BAK": "1"})
	staging := t.TempDir()
	var out bytes.Buffer
	_, err := BackupMSSQL(context.Background(), &out, "job-7", 0, "appdb",
		MSSQLOptions{StagingDir: staging, CredentialIssued: true}, t.TempDir(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SERVER side", "must name the staging constraint")
	assert.Contains(t, err.Error(), "staging_dir")
	assert.Zero(t, out.Len(), "never stream a partial on missing .bak")
}

func TestBackupMSSQLEmptyBakFails(t *testing.T) {
	fakeSQLCmd(t, map[string]string{"SQLCMD_STUB_EMPTY_BAK": "1"})
	var out bytes.Buffer
	_, err := BackupMSSQL(context.Background(), &out, "job-7", 0, "appdb",
		MSSQLOptions{CredentialIssued: true}, t.TempDir(), nil)
	require.ErrorContains(t, err, "empty (0 bytes)")
}

func TestBackupMSSQLDeletesStagingOnFailure(t *testing.T) {
	// The stub writes the .bak, then fails: the partial must still go.
	fakeSQLCmd(t, map[string]string{"SQLCMD_STUB_EXIT": "1", "SQLCMD_STUB_STDERR": "backup failed"})
	staging := t.TempDir()
	var out bytes.Buffer
	_, err := BackupMSSQL(context.Background(), &out, "job-7", 0, "appdb",
		MSSQLOptions{StagingDir: staging, CredentialIssued: true}, t.TempDir(), nil)
	require.Error(t, err)
	entries, rerr := os.ReadDir(staging)
	require.NoError(t, rerr)
	assert.Empty(t, entries, "staging .bak must be deleted on failure too")
}

func TestBackupMSSQLTLSRemediation(t *testing.T) {
	const tlsErr = "Sqlcmd: Error: ... SSL Provider: The certificate chain was issued by an authority that is not trusted"
	t.Run("without trust flag", func(t *testing.T) {
		fakeSQLCmd(t, map[string]string{"SQLCMD_STUB_EXIT": "1", "SQLCMD_STUB_STDERR": tlsErr})
		var out bytes.Buffer
		_, err := BackupMSSQL(context.Background(), &out, "job-7", 0, "appdb",
			MSSQLOptions{CredentialIssued: true}, t.TempDir(), nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "trust_server_cert=true")
	})
	t.Run("with trust flag", func(t *testing.T) {
		fakeSQLCmd(t, map[string]string{"SQLCMD_STUB_EXIT": "1", "SQLCMD_STUB_STDERR": tlsErr})
		var out bytes.Buffer
		_, err := BackupMSSQL(context.Background(), &out, "job-7", 0, "appdb",
			MSSQLOptions{TrustServerCert: true, CredentialIssued: true}, t.TempDir(), nil)
		require.Error(t, err, "the failure still fails; only the remediation is suppressed")
		assert.NotContains(t, err.Error(), "trust_server_cert=true")
	})
	t.Run("non-TLS failure has no remediation", func(t *testing.T) {
		fakeSQLCmd(t, map[string]string{"SQLCMD_STUB_EXIT": "1", "SQLCMD_STUB_STDERR": "Login failed for user 'sa'"})
		var out bytes.Buffer
		_, err := BackupMSSQL(context.Background(), &out, "job-7", 0, "appdb",
			MSSQLOptions{CredentialIssued: true}, t.TempDir(), nil)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "trust_server_cert")
	})
}

func TestBackupMSSQLNoCredentialWarns(t *testing.T) {
	logs := captureSlog(t)
	argv := fakeSQLCmd(t, nil)
	var out bytes.Buffer
	_, err := BackupMSSQL(context.Background(), &out, "job-legacy", 0, "appdb",
		MSSQLOptions{User: "sa", Password: "w4rn-flat-pw"}, t.TempDir(), nil)
	require.NoError(t, err, "fallback must keep working against old managers")
	assert.Contains(t, logs.String(), "no db_credential issued")
	assert.NotContains(t, logs.String(), "w4rn-flat-pw")
	assert.NotContains(t, readArgv(t, argv), "w4rn-flat-pw")
}

func TestParseMSSQLOptions(t *testing.T) {
	o, err := ParseMSSQLOptions(map[string]any{
		"host": "h", "port": float64(1433), "database": "d",
		"user": "u", "password": "p", "trust_server_cert": true,
		"staging_dir": "/srv/bak", "timeout_seconds": float64(60),
	})
	require.NoError(t, err)
	assert.Equal(t, "h", o.Host)
	assert.Equal(t, 1433, o.Port)
	assert.Equal(t, "d", o.Database)
	assert.True(t, o.TrustServerCert)
	assert.Equal(t, "/srv/bak", o.StagingDir)
	assert.Equal(t, time.Minute, o.Timeout)
	assert.False(t, o.CredentialIssued)

	o, err = ParseMSSQLOptions(nil)
	require.NoError(t, err)
	assert.Equal(t, DefaultDumpTimeout, o.Timeout)
	assert.False(t, o.TrustServerCert, "trust_server_cert defaults to false")
	assert.Empty(t, o.StagingDir, "staging_dir defaults to the agent work dir")
}

func TestParseMSSQLOptionsBoolForms(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want bool
	}{
		{true, true}, {"true", true}, {"True", true}, {"1", true},
		{false, false}, {"false", false}, {"0", false}, {"", false},
	} {
		o, err := ParseMSSQLOptions(map[string]any{"trust_server_cert": tc.in})
		require.NoError(t, err, "input %v", tc.in)
		assert.Equal(t, tc.want, o.TrustServerCert, "input %v", tc.in)
	}
	_, err := ParseMSSQLOptions(map[string]any{"trust_server_cert": "yes"})
	require.ErrorContains(t, err, "must be true or false")
	_, err = ParseMSSQLOptions(map[string]any{"trust_server_cert": 42})
	require.ErrorContains(t, err, "must be true or false")
}

func TestParseMSSQLOptionsDBCredential(t *testing.T) {
	o, err := ParseMSSQLOptions(map[string]any{
		"database":      "appdb",
		"db_credential": `{"username":"sa","password":"` + testIssuedPassword + `","host":"db.internal","port":"1433"}`,
	})
	require.NoError(t, err)
	assert.Equal(t, "sa", o.User)
	assert.Equal(t, testIssuedPassword, o.Password)
	assert.Equal(t, "db.internal", o.Host)
	assert.Equal(t, 1433, o.Port)
	assert.True(t, o.CredentialIssued)

	o, err = ParseMSSQLOptions(map[string]any{
		"host": "flat.local", "user": "flat", "password": "flatpw",
		"db_credential": map[string]any{"username": "issued", "password": testIssuedPassword},
	})
	require.NoError(t, err)
	assert.Equal(t, "issued", o.User, "issued identity wins")
	assert.Equal(t, testIssuedPassword, o.Password)
	assert.Equal(t, "flat.local", o.Host, "flat host wins when set")
	assert.True(t, o.CredentialIssued)

	_, err = ParseMSSQLOptions(map[string]any{
		"db_credential": map[string]any{"username": "u"},
	})
	require.ErrorContains(t, err, `"password"`)
	assert.NotContains(t, err.Error(), testIssuedPassword)
}

func TestExecutorMSSQLEndToEnd(t *testing.T) {
	fakeSQLCmd(t, map[string]string{"SQLCMD_STUB_PAYLOAD": "MSSQLBAK-E2E"})
	exec := testExecutor(t)
	job := PolledJob{ID: "job-mssql", Kind: "mssql",
		Targets: []Target{{Type: "mssql", Selector: "appdb",
			Options: map[string]any{"db_credential": `{"username":"sa","password":"` + testIssuedPassword + `"}`}}}}
	art, err := exec.ExecuteTarget(context.Background(), job, 0, job.Targets[0])
	require.NoError(t, err)
	require.NotNil(t, art)
	assert.Equal(t, TargetMSSQL, art.TargetType)
	assert.Equal(t, "t0-mssql.bak.zst", art.Name)
	assert.Equal(t, int64(len("MSSQLBAK-E2E")), art.BytesRead)

	raw, err := os.ReadFile(art.Path)
	require.NoError(t, err)
	dec, err := zstd.NewReader(bytes.NewReader(raw))
	require.NoError(t, err)
	defer dec.Close()
	var plain bytes.Buffer
	_, err = plain.ReadFrom(dec)
	require.NoError(t, err)
	assert.Equal(t, "MSSQLBAK-E2E", plain.String())
	assert.NotContains(t, plain.String(), testIssuedPassword)

	// No stray .bak left beside the pipeline artifact.
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(art.Path), "*.bak"))
	require.NoError(t, err)
	assert.Empty(t, matches)
}

func TestRestoreNoteForKind(t *testing.T) {
	note := RestoreNoteForKind("mssql")
	require.NotEmpty(t, note, "mssql restores must carry a compatibility note")
	assert.Contains(t, note, "RESTORE DATABASE")
	assert.Contains(t, note, ".bak")
	assert.Empty(t, RestoreNoteForKind("mysql"))
	assert.Empty(t, RestoreNoteForKind("postgres"))
	assert.Empty(t, RestoreNoteForKind("filesystem"))
	assert.False(t, isTarPayload("mssql", "p/t0-mssql.bak.zst"))
}

func TestRestoreMSSQLBakDelivery(t *testing.T) {
	logs := captureSlog(t)
	bak := []byte("FAKE-MSSQL-BAK-BYTES")
	var zst bytes.Buffer
	enc, err := zstd.NewWriter(&zst)
	require.NoError(t, err)
	_, err = enc.Write(bak)
	require.NoError(t, err)
	require.NoError(t, enc.Close())
	sum := sha256.Sum256(zst.Bytes())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(zst.Bytes())
	}))
	defer srv.Close()
	stub := &StubManagerClient{}
	stub.OnDownloadGrants = func(rid string, keys []string) (DownloadGrantsResponse, error) {
		return DownloadGrantsResponse{Grants: []DownloadGrant{{Key: keys[0], URL: srv.URL + "/o"}}}, nil
	}
	r := &Restorer{Manager: stub, WorkDir: t.TempDir()}
	dest := t.TempDir()
	state, err := r.Execute(context.Background(), RestoreJob{
		ID: "r-mssql", Kind: "mssql",
		Destination:     RestoreDestination{Mode: "path", Path: dest},
		OverwritePolicy: "allow",
		Objects:         []RestoreObject{{Key: "p/t0-mssql.bak.zst", SizeBytes: int64(zst.Len()), SHA256: hex.EncodeToString(sum[:])}},
	})
	require.NoError(t, err)
	assert.Equal(t, "succeeded", state)
	got, err := os.ReadFile(filepath.Join(dest, "t0-mssql.bak"))
	require.NoError(t, err)
	assert.Equal(t, bak, got, "the .bak must be delivered byte-identical")
	assert.Contains(t, logs.String(), "RESTORE DATABASE", "the compatibility note must be logged with the delivery")
}
