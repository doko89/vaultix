package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/vaultix/vaultix/internal/agent"
)

func execute(args ...string) (stdout, stderr string, err error) {
	root := newRootCommand()
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(args)
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestVersion(t *testing.T) {
	stdout, _, err := execute("version")
	require.NoError(t, err)
	assert.Contains(t, stdout, "agent "+version)
}

func TestEnrollPersistsConfigWithoutLeakingToken(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "agent.yaml")
	const token = "enroll-secret-token-xyz"
	stdout, stderr, err := execute(
		"enroll", "--config", cfgPath,
		"--manager", "https://manager.example.test",
		"--token", token,
		"--agent-id", "agent-cli-1",
	)
	require.NoError(t, err)
	assert.NotContains(t, stdout+stderr, token, "token must never appear in output")

	loaded, err := agent.Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "https://manager.example.test", loaded.ManagerURL)
	assert.Equal(t, token, loaded.Token)
	assert.Equal(t, "agent-cli-1", loaded.AgentID)
	assert.NoError(t, loaded.Validate())

	fi, err := os.Stat(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	assert.Contains(t, stdout, "agent-cli-1")
}

func TestEnrollGeneratesAgentID(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "agent.yaml")
	_, _, err := execute(
		"enroll", "--config", cfgPath,
		"--manager", "https://manager.example.test",
		"--token", "tok",
	)
	require.NoError(t, err)
	loaded, err := agent.Load(cfgPath)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(loaded.AgentID, "agent-"))
}

func TestEnrollRequiresFlags(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "agent.yaml")
	_, _, err := execute("enroll", "--config", cfgPath, "--manager", "https://m.example")
	require.ErrorContains(t, err, "--token")
	_, _, err = execute("enroll", "--config", cfgPath, "--token", "t")
	require.ErrorContains(t, err, "--manager")
	_, _, err = execute("enroll", "--config", cfgPath, "--manager", "://bad", "--token", "t")
	require.Error(t, err)
}

func TestRunRefusesUnenrolled(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "agent.yaml")
	_, _, err := execute("run", "--config", cfgPath)
	require.ErrorContains(t, err, "enroll")
}
