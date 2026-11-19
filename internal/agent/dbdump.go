package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Default dump timeout bounds pg_dump/mysqldump; jobs may override via
// options.timeout_seconds.
const DefaultDumpTimeout = 30 * time.Minute

// DumpOptions configures a database dump target. Selector is the database
// name (options.database wins when both are set). Password travels via a
// process-local env var and is never logged.
type DumpOptions struct {
	Host     string
	Port     int
	Database string
	User     string
	Password string
	Timeout  time.Duration
	// CredentialIssued reports whether User/Password came from a
	// manager-issued db_credential (short-lived, job-scoped) rather than
	// ambient configuration. Dumps without an issued credential fall back
	// to the process environment (PGPASSWORD/MYSQL_PWD, .pgpass, or trust
	// auth) with a warning — the legacy path for managers that predate
	// credential issuance. New managers refuse to dispatch such jobs, so
	// the fallback only ever fires against old managers.
	CredentialIssued bool
}

// DBCredential is a short-lived, job-scoped database credential issued by
// the manager at poll time (targets[].options.db_credential). The wire
// value is a JSON object; only username/password are required.
type DBCredential struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
}

func (o DumpOptions) argsFromSelector(selector string) DumpOptions {
	if o.Database == "" {
		o.Database = strings.TrimSpace(selector)
	}
	return o
}

func optString(opts map[string]any, key string) (string, error) {
	raw, ok := opts[key]
	if !ok || raw == nil {
		return "", nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("agent: db option %s must be a string", key)
	}
	return s, nil
}

func optInt(opts map[string]any, key string) (int, error) {
	raw, ok := opts[key]
	if !ok || raw == nil {
		return 0, nil
	}
	switch v := raw.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	case string:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &n); err != nil {
			return 0, fmt.Errorf("agent: db option %s must be a number", key)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("agent: db option %s must be a number", key)
	}
}

// ParseDumpOptions decodes postgres/mysql target options. A manager-issued
// db_credential (JSON object, or its JSON-encoded string form on the
// string-valued poll wire shape) provides the identity: its username and
// password always win, while its host/port only fill gaps the flat options
// leave unset. Parse errors name the offending field, never its value.
func ParseDumpOptions(opts map[string]any) (DumpOptions, error) {
	var out DumpOptions
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
	if raw, ok := opts["db_credential"]; ok && raw != nil {
		cred, err := parseDBCredential(raw)
		if err != nil {
			return DumpOptions{}, err
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

// parseDBCredential decodes the db_credential option: either a nested
// object or its JSON-encoded string form. Passwords are verbatim (never
// trimmed); port accepts a number or a numeric string.
func parseDBCredential(raw any) (DBCredential, error) {
	var m map[string]any
	switch v := raw.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return DBCredential{}, fmt.Errorf("agent: db_credential must not be empty")
		}
		if err := json.Unmarshal([]byte(v), &m); err != nil {
			return DBCredential{}, fmt.Errorf("agent: db_credential is not a valid credential object")
		}
	case map[string]any:
		m = v
	default:
		return DBCredential{}, fmt.Errorf("agent: db_credential must be an object")
	}
	username, err := optString(m, "username")
	if err != nil {
		return DBCredential{}, err
	}
	password, err := optString(m, "password")
	if err != nil {
		return DBCredential{}, err
	}
	host, err := optString(m, "host")
	if err != nil {
		return DBCredential{}, err
	}
	port, err := optInt(m, "port")
	if err != nil {
		return DBCredential{}, err
	}
	cred := DBCredential{
		Username: strings.TrimSpace(username),
		Password: password,
		Host:     strings.TrimSpace(host),
		Port:     port,
	}
	if cred.Username == "" {
		return DBCredential{}, fmt.Errorf("agent: db_credential is missing required field %q", "username")
	}
	if cred.Password == "" {
		return DBCredential{}, fmt.Errorf("agent: db_credential is missing required field %q", "password")
	}
	if cred.Port < 0 || cred.Port > 65535 {
		return DBCredential{}, fmt.Errorf("agent: db_credential field %q is out of range 1..65535", "port")
	}
	return cred, nil
}

// DumpPostgres runs pg_dump in custom format (-Fc), streaming stdout to w.
// The password travels via PGPASSWORD (never argv); stderr is captured
// (capped, password-scrubbed) into a structured error on failure.
func DumpPostgres(ctx context.Context, w io.Writer, jobID, selector string, opts DumpOptions, lookPath func(string) (string, error)) (bytesRead int64, err error) {
	o := opts.argsFromSelector(selector)
	if o.Database == "" {
		return 0, &ExecError{JobID: jobID, Stage: "pg_dump", Command: "pg_dump", ExitCode: -1,
			Cause: "no database selected: set target selector or options.database"}
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	bin, err := lookPath("pg_dump")
	if err != nil {
		return 0, &ExecError{JobID: jobID, Stage: "pg_dump", Command: "pg_dump", ExitCode: -1,
			Cause: "pg_dump not found on PATH: install postgresql-client"}
	}
	args := []string{"--format=custom", "--no-password"}
	if o.Host != "" {
		args = append(args, "--host="+o.Host)
	}
	if o.Port > 0 {
		args = append(args, fmt.Sprintf("--port=%d", o.Port))
	}
	if o.User != "" {
		args = append(args, "--username="+o.User)
	}
	args = append(args, o.Database)
	warnWithoutIssuedCredential(jobID, "pg_dump", o)
	env := extraEnvForPassword("PGPASSWORD", o.Password)
	return streamDump(ctx, jobID, "pg_dump", bin, args, env, o.Timeout, w, o.Password)
}

// DumpMySQL runs mysqldump, streaming stdout to w. The password travels
// via MYSQL_PWD (never argv); stderr is captured (capped,
// password-scrubbed) into a structured error on failure.
func DumpMySQL(ctx context.Context, w io.Writer, jobID, selector string, opts DumpOptions, lookPath func(string) (string, error)) (bytesRead int64, err error) {
	o := opts.argsFromSelector(selector)
	if o.Database == "" {
		return 0, &ExecError{JobID: jobID, Stage: "mysqldump", Command: "mysqldump", ExitCode: -1,
			Cause: "no database selected: set target selector or options.database"}
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	bin, err := lookPath("mysqldump")
	if err != nil {
		return 0, &ExecError{JobID: jobID, Stage: "mysqldump", Command: "mysqldump", ExitCode: -1,
			Cause: "mysqldump not found on PATH: install mysql-client"}
	}
	args := []string{"--single-transaction", "--quick"}
	if o.Host != "" {
		args = append(args, "--host="+o.Host)
	}
	if o.Port > 0 {
		args = append(args, fmt.Sprintf("--port=%d", o.Port))
	}
	if o.User != "" {
		args = append(args, "--user="+o.User)
	}
	args = append(args, o.Database)
	warnWithoutIssuedCredential(jobID, "mysqldump", o)
	env := extraEnvForPassword("MYSQL_PWD", o.Password)
	return streamDump(ctx, jobID, "mysqldump", bin, args, env, o.Timeout, w, o.Password)
}

// warnWithoutIssuedCredential logs the ambient-environment fallback: the
// job carried no manager-issued db_credential, so the dump relies on
// inherited env (PGPASSWORD/MYSQL_PWD, .pgpass) or trust auth. Identifiers
// only — never credential material.
func warnWithoutIssuedCredential(jobID, stage string, o DumpOptions) {
	if o.CredentialIssued {
		return
	}
	slog.Default().Warn("agent: no db_credential issued for database dump; falling back to ambient environment",
		"job_id", jobID, "stage", stage)
}

func extraEnvForPassword(key, password string) []string {
	if password == "" {
		return nil
	}
	return []string{key + "=" + password}
}

// streamDump runs bin with a timeout, streaming stdout to w while capturing
// the stderr tail. Arguments are fixed by the caller — never raw user
// input. password scrubs credential echoes from captured output (child
// tools may print connection strings on failure); it is never logged.
func streamDump(ctx context.Context, jobID, stage, bin string, args, extraEnv []string, timeout time.Duration, w io.Writer, password string) (int64, error) {
	if timeout <= 0 {
		timeout = DefaultDumpTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	//nolint:gosec // bin resolved via lookPath; args built from validated options.
	cmd := exec.CommandContext(ctx, bin, args...)
	armGroupKill(cmd)
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	count := &countWriter{w: w}
	tail := &tailStringWriter{max: MaxStderrBytes}
	cmd.Stdout = count
	cmd.Stderr = tail
	if err := cmd.Run(); err != nil {
		ee := &ExecError{JobID: jobID, Stage: stage, Command: stage,
			Stderr: redactSecret(tail.String(), password),
			At:     time.Now().UTC(), Cause: redactSecret(err.Error(), password)}
		if exitErr, ok := err.(*exec.ExitError); ok {
			ee.ExitCode = exitErr.ExitCode()
		} else {
			ee.ExitCode = -1
		}
		return count.n, ee
	}
	return count.n, nil
}

// redactSecret scrubs every occurrence of secret from s so child stderr
// (which may echo connection strings) can never leak credentials into
// errors, reports, or logs. Empty secrets scrub nothing.
func redactSecret(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "[REDACTED]")
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// tailStringWriter keeps at most max bytes (the tail) of stderr.
type tailStringWriter struct {
	buf []byte
	max int
}

func (w *tailStringWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.max {
		w.buf = append([]byte(nil), w.buf[len(w.buf)-w.max:]...)
	}
	return len(p), nil
}

// String returns the captured tail.
func (w *tailStringWriter) String() string { return string(w.buf) }
