package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunCommandSuccess(t *testing.T) {
	out, execErr := RunCommand(context.Background(), "job-1", "probe", "echo", "hi")
	require.Nil(t, execErr)
	assert.Equal(t, "hi\n", string(out))
}

func TestRunCommandCapturesStderr(t *testing.T) {
	_, execErr := RunCommand(context.Background(), "job-9", "pg_dump", "sh", "-c", "echo 'dump: FATAL: boom' >&2; exit 3")
	require.NotNil(t, execErr)
	assert.Equal(t, "job-9", execErr.JobID)
	assert.Equal(t, "pg_dump", execErr.Stage)
	assert.Equal(t, 3, execErr.ExitCode)
	assert.Contains(t, execErr.Stderr, "FATAL: boom")
	assert.Contains(t, execErr.Error(), "job-9")
	assert.False(t, execErr.At.IsZero())
}

func TestRunCommandStderrCapped(t *testing.T) {
	_, execErr := RunCommand(context.Background(), "job-1", "stage", "sh", "-c", "head -c 65536 /dev/zero | tr '\\0' 'z' >&2; exit 1")
	require.NotNil(t, execErr)
	assert.LessOrEqual(t, len(execErr.Stderr), MaxStderrBytes)
	assert.Greater(t, len(execErr.Stderr), 0)
}

func TestRunCommandTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, execErr := RunCommand(ctx, "job-1", "stage", "sleep", "30")
	require.NotNil(t, execErr)
	assert.True(t, strings.Contains(execErr.Cause, "killed") || strings.Contains(execErr.Cause, "deadline"),
		"cause should reflect the timeout, got %q", execErr.Cause)
}
