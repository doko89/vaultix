package agent

import (
	"context"
	"fmt"
	"io"
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

// ParseDumpOptions decodes postgres/mysql target options.
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
	out.Timeout = DefaultDumpTimeout
	if secs, err := optInt(opts, "timeout_seconds"); err != nil {
		return out, err
	} else if secs > 0 {
		out.Timeout = time.Duration(secs) * time.Second
	}
	return out, nil
}

// DumpPostgres runs pg_dump in custom format (-Fc), streaming stdout to w.
// Stderr is captured (capped) into a structured error on failure.
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
	env := extraEnvForPassword("PGPASSWORD", o.Password)
	return streamDump(ctx, jobID, "pg_dump", bin, args, env, o.Timeout, w)
}

// DumpMySQL runs mysqldump, streaming stdout to w. Stderr is captured
// (capped) into a structured error on failure.
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
	env := extraEnvForPassword("MYSQL_PWD", o.Password)
	return streamDump(ctx, jobID, "mysqldump", bin, args, env, o.Timeout, w)
}

func extraEnvForPassword(key, password string) []string {
	if password == "" {
		return nil
	}
	return []string{key + "=" + password}
}

// streamDump runs bin with a timeout, streaming stdout to w while capturing
// the stderr tail. Arguments are fixed by the caller — never raw user input.
func streamDump(ctx context.Context, jobID, stage, bin string, args, extraEnv []string, timeout time.Duration, w io.Writer) (int64, error) {
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
			Stderr: tail.String(), At: time.Now().UTC(), Cause: err.Error()}
		if exitErr, ok := err.(*exec.ExitError); ok {
			ee.ExitCode = exitErr.ExitCode()
		} else {
			ee.ExitCode = -1
		}
		return count.n, ee
	}
	return count.n, nil
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
