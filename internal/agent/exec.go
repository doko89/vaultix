package agent

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

// MaxStderrBytes caps captured stderr so a chatty subprocess cannot exhaust
// agent memory. The tail is kept: the actionable error is usually last.
const MaxStderrBytes = 8 << 10

// ExecError is the structured error for database-tool shell-outs
// (pg_dump, mysqldump, ...). Helpers only — no real dump is wired yet.
type ExecError struct {
	JobID    string    `json:"job_id"`
	Stage    string    `json:"stage"`
	Command  string    `json:"command"`
	ExitCode int       `json:"exit_code"`
	Stderr   string    `json:"stderr"`
	At       time.Time `json:"at"`
	Cause    string    `json:"cause"`
}

func (e *ExecError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("agent: job %s stage %s (%s) failed with exit %d: %s",
			e.JobID, e.Stage, e.Command, e.ExitCode, e.Stderr)
	}
	return fmt.Sprintf("agent: job %s stage %s (%s) failed with exit %d: %s",
		e.JobID, e.Stage, e.Command, e.ExitCode, e.Cause)
}

// RunCommand executes name with a context timeout, streaming stdout to the
// caller while capturing (capped) stderr for structured errors. Arguments
// must come from validated job data, never raw user input.
func RunCommand(ctx context.Context, jobID, stage, name string, args ...string) (stdout []byte, execErr *ExecError) {
	//nolint:gosec // name/args are caller-provided from validated jobs.
	cmd := exec.CommandContext(ctx, name, args...)
	armGroupKill(cmd)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &tailWriter{buf: &errBuf, max: MaxStderrBytes}
	err := cmd.Run()
	if err == nil {
		return outBuf.Bytes(), nil
	}
	ee := &ExecError{
		JobID:   jobID,
		Stage:   stage,
		Command: name,
		Stderr:  errBuf.String(),
		At:      time.Now().UTC(),
		Cause:   err.Error(),
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		ee.ExitCode = exitErr.ExitCode()
	} else {
		ee.ExitCode = -1
	}
	return outBuf.Bytes(), ee
}

// tailWriter keeps at most max bytes (the tail) of everything written.
type tailWriter struct {
	buf *bytes.Buffer
	max int
}

func (w *tailWriter) Write(p []byte) (int, error) {
	if len(p) >= w.max {
		w.buf.Reset()
		return w.buf.Write(p[len(p)-w.max:])
	}
	if overflow := w.buf.Len() + len(p) - w.max; overflow > 0 {
		w.buf.Next(overflow)
	}
	return w.buf.Write(p)
}
