package agent

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MSSQL native full backup (PRD 7.7; differential/log stay post-MVP).
//
// STAGING CONSTRAINT: BACKUP ... TO DISK writes on the SERVER side, so the
// staging path must be readable by the agent process. Supported layouts:
// co-located SQL Server + agent (same filesystem), a shared volume mounted
// at the same path on both, or an admin-arranged path the agent can read.
// When the .bak is missing or unreadable after sqlcmd succeeds, the job
// fails with an actionable error naming this constraint — never a silent
// success, never a partial upload.

// Capability names advertised to the manager (see requiredCapabilities in
// internal/usecase/job: "docker" gates docker targets, "mssql" gates mssql
// targets; every other target type is always eligible).
const (
	CapabilityDocker = "docker"
	CapabilityMSSQL  = "mssql"
)

// mssqlProbeTimeout bounds the `sqlcmd -?` version probe so capability
// detection can never hang the agent start.
const mssqlProbeTimeout = 10 * time.Second

// MSSQLOptions configures one MSSQL native-full backup target. Selector is
// the database name (options.database wins when both are set). Password
// travels via SQLCMDPASSWORD and is never logged.
type MSSQLOptions struct {
	Host     string
	Port     int
	Database string
	User     string
	Password string
	// TrustServerCert passes sqlcmd -C (trust the server certificate).
	// Default false: encrypted connections validate the server cert.
	TrustServerCert bool
	// StagingDir is where BACKUP ... TO DISK writes the .bak. Default is
	// the agent's per-job work dir. See the STAGING CONSTRAINT above: the
	// path must be readable by this agent process.
	StagingDir string
	Timeout    time.Duration
	// CredentialIssued mirrors DumpOptions: true when User/Password came
	// from a manager-issued db_credential rather than ambient config.
	CredentialIssued bool
}

func (o MSSQLOptions) argsFromSelector(selector string) MSSQLOptions {
	if o.Database == "" {
		o.Database = strings.TrimSpace(selector)
	}
	return o
}

func optBool(opts map[string]any, key string) (bool, error) {
	raw, ok := opts[key]
	if !ok || raw == nil {
		return false, nil
	}
	switch v := raw.(type) {
	case bool:
		return v, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1":
			return true, nil
		case "false", "0", "":
			return false, nil
		default:
			return false, fmt.Errorf("agent: db option %s must be true or false", key)
		}
	default:
		return false, fmt.Errorf("agent: db option %s must be true or false", key)
	}
}

// ParseMSSQLOptions decodes mssql target options. Credential semantics match
// ParseDumpOptions exactly: a manager-issued db_credential (object or its
// JSON-encoded string form) provides the identity, its username/password
// always win, its host/port fill unset gaps. Parse errors name the field,
// never its value.
func ParseMSSQLOptions(opts map[string]any) (MSSQLOptions, error) {
	var out MSSQLOptions
	var err error
	if out.Host, err = optString(opts, "host"); err != nil {
		return out, err
	}
	if out.Port, err = optInt(opts, "port"); err != nil {
		return out, err
	}
	if out.Database, err = optString(opts, "database"); err != nil {
		return out, err
	}
	if out.User, err = optString(opts, "user"); err != nil {
		return out, err
	}
	if out.Password, err = optString(opts, "password"); err != nil {
		return out, err
	}
	if out.TrustServerCert, err = optBool(opts, "trust_server_cert"); err != nil {
		return out, err
	}
	if out.StagingDir, err = optString(opts, "staging_dir"); err != nil {
		return out, err
	}
	if raw, ok := opts["db_credential"]; ok && raw != nil {
		cred, err := parseDBCredential(raw)
		if err != nil {
			return MSSQLOptions{}, err
		}
		out.User, out.Password = cred.Username, cred.Password
		if out.Host == "" {
			out.Host = cred.Host
		}
		if out.Port == 0 {
			out.Port = cred.Port
		}
		out.CredentialIssued = true
	}
	out.Timeout = DefaultDumpTimeout
	if secs, err := optInt(opts, "timeout_seconds"); err != nil {
		return out, err
	} else if secs > 0 {
		out.Timeout = time.Duration(secs) * time.Second
	}
	return out, nil
}

// ValidateMSSQLIdentifier rejects anything that is not identifier-ish, at
// the boundary, before the name reaches T-SQL. This kills T-SQL injection:
// statement terminators, comment openers, quotes, whitespace, path
// separators, and control bytes are all refused. Errors echo the rejected
// shape only in safe cases (empty); otherwise they name the reason.
func ValidateMSSQLIdentifier(db string) error {
	if db == "" {
		return fmt.Errorf("agent: mssql: no database selected: set target selector or options.database")
	}
	if strings.Contains(db, "--") {
		return fmt.Errorf("agent: mssql: database name must be a plain identifier (comment opener \"--\" is not allowed)")
	}
	if strings.ContainsAny(db, ";'\"`") {
		return fmt.Errorf("agent: mssql: database name must be a plain identifier (quotes and statement terminators are not allowed)")
	}
	if strings.ContainsAny(db, "/\\\x00") {
		return fmt.Errorf("agent: mssql: database name must be a plain identifier, not a path")
	}
	for _, r := range db {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\v' || r == '\f' || r < 0x20 || r == 0x7f {
			return fmt.Errorf("agent: mssql: database name must be a plain identifier (whitespace and control characters are not allowed)")
		}
	}
	return nil
}

// MSSQLBackupTSQL renders the native full backup statement. The database is
// bracket-quoted (] escaped to ]]); the DISK path is an N” literal (”
// escaped). Exact template:
//
//	BACKUP DATABASE [<db>] TO DISK = N'<staging>' WITH COMPRESSION, CHECKSUM, INIT, STATS = 10
func MSSQLBackupTSQL(db, stagingPath string) string {
	quoted := "[" + strings.ReplaceAll(db, "]", "]]") + "]"
	literal := "N'" + strings.ReplaceAll(stagingPath, "'", "''") + "'"
	return fmt.Sprintf("BACKUP DATABASE %s TO DISK = %s WITH COMPRESSION, CHECKSUM, INIT, STATS = 10", quoted, literal)
}

// ProbeSQLCmd reports whether sqlcmd is usable: present on PATH and
// answering the `sqlcmd -?` version probe with exit 0 inside a short
// timeout. A nil lookPath uses exec.LookPath.
func ProbeSQLCmd(lookPath func(string) (string, error)) bool {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	bin, err := lookPath("sqlcmd")
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), mssqlProbeTimeout)
	defer cancel()
	//nolint:gosec // bin resolved via lookPath; -? prints version/usage only.
	cmd := exec.CommandContext(ctx, bin, "-?")
	armGroupKill(cmd)
	if err := cmd.Run(); err != nil {
		return false
	}
	return ctx.Err() == nil
}

// sqlcmdAvailable caches the version probe for the process: run start probes
// once, every later call reuses the result.
var sqlcmdAvailable = sync.OnceValue(func() bool { return ProbeSQLCmd(nil) })

// SQLCmdAvailable reports the cached `sqlcmd -?` probe result.
func SQLCmdAvailable() bool { return sqlcmdAvailable() }

// detectCapabilities is the pure core of DetectCapabilities (seam for
// deterministic tests).
func detectCapabilities(dockerEnabled, sqlcmdPresent bool) []string {
	var out []string
	if dockerEnabled {
		out = append(out, CapabilityDocker)
	}
	if sqlcmdPresent {
		out = append(out, CapabilityMSSQL)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// DetectCapabilities computes the heartbeat capability advertisement: "docker"
// when Docker integration is enabled, "mssql" when sqlcmd probed present.
// Nil (no row update by the manager) when nothing is detected.
func DetectCapabilities(dockerEnabled bool) []string {
	return detectCapabilities(dockerEnabled, SQLCmdAvailable())
}

// LogCapabilities logs the capability probe decision (identifiers only).
func LogCapabilities(log *slog.Logger, dockerEnabled bool) []string {
	caps := DetectCapabilities(dockerEnabled)
	if log == nil {
		log = slog.Default()
	}
	log.Info("capability probe complete",
		"docker_enabled", dockerEnabled,
		"sqlcmd_present", SQLCmdAvailable(),
		"capabilities", caps)
	return caps
}

// BackupMSSQL runs a native full backup via sqlcmd and streams the resulting
// .bak into zw (the compress→encrypt→upload pipeline writes through it),
// returning pre-compression bytes. workDir is the agent's per-job staging
// root (used when options carry no staging_dir); lookPath is a seam for
// tests (nil uses exec.LookPath).
//
// Flow: BACKUP DATABASE ... TO DISK → verify the .bak exists and is
// non-empty → stream it → delete the staging file on success AND failure
// (best-effort, logged). The password travels via SQLCMDPASSWORD (never
// argv); stderr is captured (capped, password-scrubbed) into a structured
// error on failure.
func BackupMSSQL(ctx context.Context, zw io.Writer, jobID string, targetIdx int, selector string, opts MSSQLOptions, workDir string, lookPath func(string) (string, error)) (bytesRead int64, err error) {
	o := opts.argsFromSelector(selector)
	if err := ValidateMSSQLIdentifier(o.Database); err != nil {
		return 0, &ExecError{JobID: jobID, Stage: "sqlcmd", Command: "sqlcmd", ExitCode: -1, Cause: err.Error()}
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	bin, err := lookPath("sqlcmd")
	if err != nil {
		return 0, &ExecError{JobID: jobID, Stage: "sqlcmd", Command: "sqlcmd", ExitCode: -1,
			Cause: "sqlcmd not found on PATH: install mssql-tools (https://learn.microsoft.com/sql/tools/sqlcmd) and ensure sqlcmd is on PATH"}
	}
	stagingDir := strings.TrimSpace(o.StagingDir)
	if stagingDir == "" {
		stagingDir = workDir
	}
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return 0, &ExecError{JobID: jobID, Stage: "sqlcmd", Command: "sqlcmd", ExitCode: -1,
			Cause: fmt.Sprintf("create mssql staging dir: %v", err)}
	}
	// The database name is identifier-safe (validated above), so embedding
	// it in the staging file name cannot escape the staging dir.
	bakPath := filepath.Join(stagingDir, fmt.Sprintf("t%d-%s.bak", targetIdx, o.Database))
	// The staging .bak is deleted on success AND failure (best-effort):
	// it may hold a full database copy in cleartext.
	defer func() {
		if err := os.Remove(bakPath); err != nil && !os.IsNotExist(err) {
			slog.Default().Warn("agent: mssql: remove staging .bak failed",
				"job_id", jobID, "path", bakPath, "error", err)
		}
	}()

	query := MSSQLBackupTSQL(o.Database, bakPath)
	args := []string{"-b", "-S", mssqlServer(o.Host, o.Port)}
	if o.User != "" {
		args = append(args, "-U", o.User)
	}
	args = append(args, "-Q", query)
	if o.TrustServerCert {
		args = append(args, "-C")
	}
	warnWithoutIssuedCredential(jobID, "sqlcmd", DumpOptions{CredentialIssued: o.CredentialIssued})
	if err := runSQLCmd(ctx, jobID, bin, args, o.Password, o.Timeout); err != nil {
		return 0, err
	}
	info, err := os.Stat(bakPath)
	if err != nil || info.Size() == 0 {
		reason := "BACKUP reported success but the staging .bak is missing or unreadable"
		if err == nil {
			reason = "BACKUP reported success but the staging .bak is empty (0 bytes)"
		}
		return 0, &ExecError{JobID: jobID, Stage: "sqlcmd", Command: "sqlcmd", ExitCode: -1,
			Cause: reason + fmt.Sprintf("; BACKUP ... TO DISK writes on the SERVER side, so the staging path %q must be readable by this agent "+
				"(co-locate SQL Server with the agent, share a volume at the same path, or set target option staging_dir to an agent-readable path)", bakPath)}
	}
	f, err := os.Open(bakPath)
	if err != nil {
		return 0, &ExecError{JobID: jobID, Stage: "sqlcmd", Command: "sqlcmd", ExitCode: -1,
			Cause: fmt.Sprintf("open staging .bak %q: %v (BACKUP ... TO DISK writes on the SERVER side; the path must be readable by this agent)", bakPath, err)}
	}
	n, copyErr := io.Copy(zw, f)
	closeErr := f.Close()
	if copyErr != nil {
		return n, &ExecError{JobID: jobID, Stage: "sqlcmd", Command: "sqlcmd", ExitCode: -1,
			Cause: fmt.Sprintf("read staging .bak: %v", copyErr)}
	}
	if closeErr != nil {
		return n, &ExecError{JobID: jobID, Stage: "sqlcmd", Command: "sqlcmd", ExitCode: -1,
			Cause: fmt.Sprintf("close staging .bak: %v", closeErr)}
	}
	return n, nil
}

// mssqlServer renders the sqlcmd -S value. Empty host + default port omits
// the flag value's host part (sqlcmd defaults to the local default
// instance); a non-default port rides as host,port.
func mssqlServer(host string, port int) string {
	h := strings.TrimSpace(host)
	if h == "" {
		h = "localhost"
	}
	if port > 0 && port != 1433 {
		return fmt.Sprintf("%s,%d", h, port)
	}
	return h
}

// runSQLCmd executes sqlcmd with a context timeout and process-group kill,
// capturing the capped stderr tail. extraEnv carries SQLCMDPASSWORD only
// (never argv). Failures return a password-scrubbed ExecError; TLS/-cert
// failures without trust_server_cert carry the actionable remediation.
func runSQLCmd(ctx context.Context, jobID, bin string, args []string, password string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = DefaultDumpTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	//nolint:gosec // bin resolved via lookPath; args built from validated options.
	cmd := exec.CommandContext(ctx, bin, args...)
	armGroupKill(cmd)
	if password != "" {
		cmd.Env = append(os.Environ(), "SQLCMDPASSWORD="+password)
	}
	tail := &tailStringWriter{max: MaxStderrBytes}
	cmd.Stdout = io.Discard // BACKUP progress (STATS=10) is noise; the .bak is the payload
	cmd.Stderr = tail
	if err := cmd.Run(); err != nil {
		stderr := redactSecret(tail.String(), password)
		cause := redactSecret(err.Error(), password)
		ee := &ExecError{JobID: jobID, Stage: "sqlcmd", Command: "sqlcmd",
			Stderr: stderr, At: time.Now().UTC(), Cause: cause}
		if exitErr, ok := err.(*exec.ExitError); ok {
			ee.ExitCode = exitErr.ExitCode()
		} else {
			ee.ExitCode = -1
		}
		if note := tlsRemediation(stderr, args); note != "" {
			ee.Stderr = joinNonEmpty(stderr, note)
		}
		return ee
	}
	return nil
}

// tlsRemediation spots TLS/certificate failures when -C was NOT passed and
// returns the actionable remediation. Empty when -C is already set or the
// stderr shows no TLS shape.
func tlsRemediation(stderr string, args []string) string {
	for _, a := range args {
		if a == "-C" {
			return ""
		}
	}
	lower := strings.ToLower(stderr)
	for _, marker := range []string{
		"certificate", "self signed", "self-signed", "ssl", "tls",
		"trust server certificate", "trustservercertificate",
		"error: 20", "error:20", "handshake",
	} {
		if strings.Contains(lower, marker) {
			return "sqlcmd TLS certificate validation failed: if this SQL Server uses a self-signed " +
				"certificate, set target option trust_server_cert=true (passes sqlcmd -C) or install a " +
				"trusted server certificate"
		}
	}
	return ""
}

func joinNonEmpty(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "\n" + b
}

// MSSQLRestoreNote documents the deferred-reimport pattern shared with the
// pg/mysql dumps: Vaultix restores the native .bak as a FILE; the operator
// re-imports it into SQL Server with RESTORE DATABASE.
const MSSQLRestoreNote = "mssql restore delivers the native full-backup .bak as a file; " +
	"re-import it with RESTORE DATABASE [<database>] FROM DISK = '<delivered .bak>' " +
	"(operator step; differential/log restores stay post-MVP)"

// RestoreNoteForKind returns the operator-facing restore note for kinds that
// need one (mssql), or "" when the kind restores in place without follow-up.
func RestoreNoteForKind(kind string) string {
	if NormalizeTargetType(kind) == TargetMSSQL {
		return MSSQLRestoreNote
	}
	return ""
}
