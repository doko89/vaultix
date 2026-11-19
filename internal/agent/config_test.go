package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.yaml")
	cfg := DefaultConfig()
	cfg.ManagerURL = "https://manager.example.test"
	cfg.Token = "tok-123"
	cfg.AgentID = "agent-7"

	require.NoError(t, cfg.Save(path))
	fi, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())

	loaded, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, cfg.ManagerURL, loaded.ManagerURL)
	assert.Equal(t, cfg.Token, loaded.Token)
	assert.Equal(t, cfg.AgentID, loaded.AgentID)
	assert.Equal(t, cfg.HeartbeatInterval, loaded.HeartbeatInterval)
	assert.Equal(t, cfg.RequestTimeout, loaded.RequestTimeout)
	assert.NoError(t, loaded.Validate())
}

func TestConfigValidateRequiresEnrollment(t *testing.T) {
	assert.Error(t, DefaultConfig().Validate())
	bad := DefaultConfig()
	bad.ManagerURL = "://bad"
	bad.Token = "t"
	bad.AgentID = "a"
	assert.Error(t, bad.Validate())
}

func TestConfigRedacted(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Token = "super-secret"
	assert.Equal(t, "***", cfg.Redacted().Token)
	assert.Equal(t, "super-secret", cfg.Token, "Redacted must not mutate")
}

func TestLoadMissingFileYieldsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	require.NoError(t, err)
	assert.Equal(t, DefaultConfig().HeartbeatInterval, cfg.HeartbeatInterval)
	assert.Error(t, cfg.Validate(), "defaults alone must not validate without enrollment")
}
