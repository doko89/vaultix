// Package agent holds the Vaultix agent runtime: heartbeat, job polling,
// local state, discovery, and streaming archive helpers.
package agent

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config keys used in the agent config file and environment.
const (
	KeyManagerURL          = "manager_url"
	KeyToken               = "token"
	KeyAgentID             = "agent_id"
	KeyHeartbeatInterval   = "heartbeat_interval"
	KeyPollInterval        = "poll_interval"
	KeyRestorePollInterval = "restore_poll_interval"
	KeyStateFile           = "state_file"
	KeyWorkDir             = "work_dir"
	KeyDockerEnabled       = "docker_enabled"
	KeyDockerHost          = "docker_host"
	KeyRequestTimeout      = "request_timeout"
	KeyCompressionLevel    = "compression_level"
	redacted               = "***"
	defaultRequestTimeout  = 15 * time.Second
	defaultPollInterval    = 15 * time.Second
)

// Config is the agent runtime configuration. Token is a credential and must
// never be logged; use Redacted for any log output.
type Config struct {
	ManagerURL          string        `mapstructure:"manager_url"`
	Token               string        `mapstructure:"token"`
	AgentID             string        `mapstructure:"agent_id"`
	HeartbeatInterval   time.Duration `mapstructure:"heartbeat_interval"`
	PollInterval        time.Duration `mapstructure:"poll_interval"`
	RestorePollInterval time.Duration `mapstructure:"restore_poll_interval"`
	StateFile           string        `mapstructure:"state_file"`
	WorkDir             string        `mapstructure:"work_dir"`
	DockerEnabled       bool          `mapstructure:"docker_enabled"`
	DockerHost          string        `mapstructure:"docker_host"`
	RequestTimeout      time.Duration `mapstructure:"request_timeout"`
	CompressionLevel    int           `mapstructure:"compression_level"`
}

// DefaultConfig returns sane defaults. ManagerURL, Token, and AgentID have no
// defaults and must come from enrollment.
func DefaultConfig() Config {
	return Config{
		HeartbeatInterval:   30 * time.Second,
		PollInterval:        defaultPollInterval,
		RestorePollInterval: defaultPollInterval,
		StateFile:           filepath.Join(userConfigDir(), "vaultix", "agent-state.json"),
		WorkDir:             filepath.Join(os.TempDir(), "vaultix-agent"),
		DockerEnabled:       false,
		DockerHost:          "",
		RequestTimeout:      defaultRequestTimeout,
		CompressionLevel:    3,
	}
}

// Validate rejects malformed or incomplete configuration at the boundary.
func (c Config) Validate() error {
	if strings.TrimSpace(c.ManagerURL) == "" {
		return fmt.Errorf("agent: manager_url is required (run 'agent enroll' first)")
	}
	u, err := url.Parse(c.ManagerURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("agent: invalid manager_url %q", c.ManagerURL)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("agent: manager_url scheme must be https (http allowed only for local dev): %q", c.ManagerURL)
	}
	if strings.TrimSpace(c.Token) == "" {
		return fmt.Errorf("agent: token is required (run 'agent enroll' first)")
	}
	if strings.TrimSpace(c.AgentID) == "" {
		return fmt.Errorf("agent: agent_id is required (run 'agent enroll' first)")
	}
	if c.HeartbeatInterval <= 0 {
		return fmt.Errorf("agent: heartbeat_interval must be positive")
	}
	if c.PollInterval <= 0 {
		return fmt.Errorf("agent: poll_interval must be positive")
	}
	if c.RestorePollInterval <= 0 {
		return fmt.Errorf("agent: restore_poll_interval must be positive")
	}
	if c.RequestTimeout <= 0 {
		return fmt.Errorf("agent: request_timeout must be positive")
	}
	if strings.TrimSpace(c.StateFile) == "" {
		return fmt.Errorf("agent: state_file is required")
	}
	return nil
}

// Redacted returns a copy safe for logging: the credential is masked.
func (c Config) Redacted() Config {
	c.Token = redacted
	return c
}

// Load reads configuration from path (when non-empty) layered over defaults,
// with VAULTIX_AGENT_* environment overrides. A missing file is not an error;
// Validate reports what enrollment still needs to provide.
func Load(path string) (Config, error) {
	v := viper.New()
	def := DefaultConfig()
	v.SetDefault(KeyHeartbeatInterval, def.HeartbeatInterval)
	v.SetDefault(KeyPollInterval, def.PollInterval)
	v.SetDefault(KeyRestorePollInterval, def.RestorePollInterval)
	v.SetDefault(KeyStateFile, def.StateFile)
	v.SetDefault(KeyWorkDir, def.WorkDir)
	v.SetDefault(KeyDockerEnabled, def.DockerEnabled)
	v.SetDefault(KeyDockerHost, def.DockerHost)
	v.SetDefault(KeyRequestTimeout, def.RequestTimeout)
	v.SetDefault(KeyCompressionLevel, def.CompressionLevel)
	v.SetEnvPrefix("VAULTIX_AGENT")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			// ConfigFileNotFoundError covers search-path lookup; an explicit
			// SetConfigFile yields a plain *os.PathError instead.
			if _, ok := err.(viper.ConfigFileNotFoundError); !ok && !os.IsNotExist(err) {
				return Config{}, fmt.Errorf("agent: read config %s: %w", path, err)
			}
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return Config{}, fmt.Errorf("agent: decode config: %w", err)
	}
	return cfg, nil
}

// Save persists the configuration to path as YAML. The file holds a
// credential, so the parent directory is created and the file is chmod 0600.
// The write is crash-safe (temp file + rename).
func (c Config) Save(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("agent: config path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("agent: create config dir: %w", err)
	}
	v := viper.New()
	v.Set(KeyManagerURL, c.ManagerURL)
	v.Set(KeyToken, c.Token)
	v.Set(KeyAgentID, c.AgentID)
	v.Set(KeyHeartbeatInterval, c.HeartbeatInterval.String())
	v.Set(KeyPollInterval, c.PollInterval.String())
	v.Set(KeyRestorePollInterval, c.RestorePollInterval.String())
	v.Set(KeyStateFile, c.StateFile)
	v.Set(KeyWorkDir, c.WorkDir)
	v.Set(KeyDockerEnabled, c.DockerEnabled)
	v.Set(KeyDockerHost, c.DockerHost)
	v.Set(KeyRequestTimeout, c.RequestTimeout.String())
	v.Set(KeyCompressionLevel, c.CompressionLevel)

	// The temp file keeps a .yaml suffix so viper detects the format.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agent-config-*.yaml")
	if err != nil {
		return fmt.Errorf("agent: create temp config: %w", err)
	}
	tmpName := tmp.Name()
	_ = tmp.Close()
	//nolint:gosec // credential file must be owner-only.
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("agent: chmod temp config: %w", err)
	}
	if err := v.WriteConfigAs(tmpName); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("agent: write temp config: %w", err)
	}
	//nolint:gosec // credential file must stay owner-only after rename.
	if err := os.Chmod(tmpName, 0o600); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("agent: chmod temp config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("agent: install config: %w", err)
	}
	return nil
}

func userConfigDir() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return dir
	}
	return "."
}
