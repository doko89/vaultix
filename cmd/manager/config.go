package main

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// S3Config is the Manager-side object-storage configuration. Agents receive
// only short-lived scoped grants (storage track); these long-lived values
// never leave the Manager.
type S3Config struct {
	Endpoint  string `mapstructure:"endpoint"`
	Bucket    string `mapstructure:"bucket"`
	Region    string `mapstructure:"region"`
	AccessKey string `mapstructure:"access_key"`
	SecretKey string `mapstructure:"secret_key"`
	UseSSL    bool   `mapstructure:"use_ssl"`
}

// Configured reports whether enough is set to talk to object storage.
func (c S3Config) Configured() bool {
	return strings.TrimSpace(c.Endpoint) != "" && strings.TrimSpace(c.Bucket) != ""
}

// SMTPConfig mirrors notification.SMTPConfig in config-file-friendly types
// (seconds instead of durations; see serve.go for the conversion).
type SMTPConfig struct {
	Host            string `mapstructure:"host"`
	Port            int    `mapstructure:"port"`
	From            string `mapstructure:"from"`
	Auth            string `mapstructure:"auth"`
	Username        string `mapstructure:"username"`
	Password        string `mapstructure:"password"`
	TLS             string `mapstructure:"tls"`
	DialTimeoutSecs int    `mapstructure:"dial_timeout_seconds"`
	IOTimeoutSecs   int    `mapstructure:"io_timeout_seconds"`
}

// Config is the Manager configuration: YAML file + VAULTIX_* env + flags.
// Precedence (high to low): flags, env, file, defaults.
type Config struct {
	Addr                 string `mapstructure:"addr"`
	DatabaseURL          string `mapstructure:"database_url"`
	MigrationsDir        string `mapstructure:"migrations_dir"`
	StaticDir            string `mapstructure:"static_dir"`
	LogLevel             string `mapstructure:"log_level"`
	RequestTimeoutSecs   int    `mapstructure:"request_timeout_seconds"`
	ShutdownTimeoutSecs  int    `mapstructure:"shutdown_timeout_seconds"`
	HeartbeatTimeoutSecs int    `mapstructure:"heartbeat_timeout_seconds"`
	// OIDCRedirectURL is the absolute IdP callback URL (empty disables
	// OIDC login starts at runtime).
	OIDCRedirectURL string `mapstructure:"oidc_redirect_url"`
	// DispatchIntervalSecs ticks schedule dispatch + notification drain.
	DispatchIntervalSecs int `mapstructure:"dispatch_interval_seconds"`
	// JanitorIntervalSecs ticks expired-session cleanup.
	JanitorIntervalSecs int `mapstructure:"janitor_interval_seconds"`

	S3   S3Config   `mapstructure:"s3"`
	SMTP SMTPConfig `mapstructure:"smtp"`
}

// RequestTimeout returns the HTTP request timeout.
func (c Config) RequestTimeout() time.Duration {
	if c.RequestTimeoutSecs <= 0 {
		return 30 * time.Second
	}
	return time.Duration(c.RequestTimeoutSecs) * time.Second
}

// ShutdownTimeout returns the graceful-shutdown budget.
func (c Config) ShutdownTimeout() time.Duration {
	if c.ShutdownTimeoutSecs <= 0 {
		return 15 * time.Second
	}
	return time.Duration(c.ShutdownTimeoutSecs) * time.Second
}

// DispatchInterval returns the schedule-dispatch tick.
func (c Config) DispatchInterval() time.Duration {
	if c.DispatchIntervalSecs <= 0 {
		return 30 * time.Second
	}
	return time.Duration(c.DispatchIntervalSecs) * time.Second
}

// JanitorInterval returns the session-cleanup tick.
func (c Config) JanitorInterval() time.Duration {
	if c.JanitorIntervalSecs <= 0 {
		return 5 * time.Minute
	}
	return time.Duration(c.JanitorIntervalSecs) * time.Second
}

// Validate checks required fields.
func (c Config) Validate() error {
	if strings.TrimSpace(c.DatabaseURL) == "" {
		return fmt.Errorf("database_url is required (flag --database-url, env VAULTIX_DATABASE_URL, or config file)")
	}
	switch strings.ToLower(c.LogLevel) {
	case "", "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid log_level %q", c.LogLevel)
	}
	return nil
}

// Redacted returns a log-safe copy: database password, S3 secret, and SMTP
// password are masked.
func (c Config) Redacted() Config {
	out := c
	if u, err := url.Parse(out.DatabaseURL); err == nil && u.User != nil {
		u.User = url.UserPassword(u.User.Username(), "***")
		out.DatabaseURL = u.String()
	} else if out.DatabaseURL != "" {
		out.DatabaseURL = "***"
	}
	if out.S3.SecretKey != "" {
		out.S3.SecretKey = "***"
	}
	if out.SMTP.Password != "" {
		out.SMTP.Password = "***"
	}
	return out
}

// flagKeys maps viper keys to flag names.
var flagKeys = [][2]string{
	{"addr", "addr"},
	{"database_url", "database-url"},
	{"migrations_dir", "migrations-dir"},
	{"static_dir", "static-dir"},
	{"log_level", "log-level"},
	{"request_timeout_seconds", "request-timeout-seconds"},
	{"shutdown_timeout_seconds", "shutdown-timeout-seconds"},
}

// registerFlags declares the shared CLI flags on cmd (persistent when
// asked). It works through *cobra.Command so this package never imports
// pflag directly (pflag is an indirect dependency; go.mod is frozen for
// this track).
func registerFlags(cmd *cobra.Command, persistent bool) {
	fs := cmd.Flags()
	if persistent {
		fs = cmd.PersistentFlags()
	}
	fs.String("addr", ":8080", "HTTP listen address")
	fs.String("database-url", "", "Postgres URL (env VAULTIX_DATABASE_URL)")
	fs.String("migrations-dir", "db/migrations", "SQL migration directory")
	fs.String("static-dir", "./web/dist", "built web UI directory (from `npm --prefix web run build`)")
	fs.String("log-level", "info", "log level: debug|info|warn|error")
	fs.Int("request-timeout-seconds", 30, "HTTP request timeout")
	fs.Int("shutdown-timeout-seconds", 15, "graceful shutdown budget")
}

// bindRunFlags binds the EXECUTING command's parsed FlagSet to viper. It
// must run inside RunE (not at construction): serve and migrate declare the
// same keys on different FlagSets, and binding both upfront would let the
// unparsed set shadow the parsed one.
func bindRunFlags(cmd *cobra.Command) {
	for _, pair := range flagKeys {
		if f := cmd.Flags().Lookup(pair[1]); f != nil {
			_ = viper.BindPFlag(pair[0], f)
		}
	}
}

// loadConfig reads file (optional) + env + flags into Config using the
// global viper (flags bound via bindFlags). An explicit --config path that
// cannot be read is an error; otherwise a missing file is fine
// (env/flags/defaults apply).
func loadConfig(configPath string) (Config, error) {
	viper.SetConfigName("manager")
	viper.SetConfigType("yaml")
	if configPath != "" {
		viper.SetConfigFile(configPath)
	} else {
		viper.AddConfigPath(".")
		viper.AddConfigPath("/etc/vaultix")
	}
	viper.SetEnvPrefix("VAULTIX")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
	// Nested keys must be bound explicitly: Unmarshal only consults env
	// for known keys, and nested structs have no flag bindings.
	for _, key := range []string{
		"s3.endpoint", "s3.bucket", "s3.region", "s3.access_key", "s3.secret_key", "s3.use_ssl",
		"smtp.host", "smtp.port", "smtp.from", "smtp.auth", "smtp.username",
		"smtp.password", "smtp.tls", "smtp.dial_timeout_seconds", "smtp.io_timeout_seconds",
	} {
		_ = viper.BindEnv(key)
	}

	viper.SetDefault("addr", ":8080")
	viper.SetDefault("migrations_dir", "db/migrations")
	viper.SetDefault("static_dir", "./web/dist")
	viper.SetDefault("log_level", "info")
	viper.SetDefault("request_timeout_seconds", 30)
	viper.SetDefault("shutdown_timeout_seconds", 15)
	viper.SetDefault("heartbeat_timeout_seconds", 300)
	viper.SetDefault("dispatch_interval_seconds", 30)
	viper.SetDefault("janitor_interval_seconds", 300)

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
		if configPath != "" {
			return Config{}, fmt.Errorf("read config %s: %w", configPath, err)
		}
		// No file found and none demanded: env/flags/defaults apply.
	}
	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	return cfg, nil
}
