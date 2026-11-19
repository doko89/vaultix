package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// Environment overrides for the CLI credentials. They win over the config
// file so scripts and CI can inject short-lived keys without touching disk.
const (
	envManagerURL = "VAULTIX_MANAGER_URL"
	envAPIKey     = "VAULTIX_API_KEY"
)

// fileConfig is the ~/.config/vaultix/cli.yaml shape: the Manager base URL
// plus the user's API key.
type fileConfig struct {
	ManagerURL string `mapstructure:"manager_url"`
	APIKey     string `mapstructure:"api_key"`
}

// resolvedConfig is the effective configuration: file values with env
// overrides applied, plus the path the file values came from.
type resolvedConfig struct {
	path       string
	ManagerURL string
	APIKey     string
}

// defaultConfigPath is used when --config is not given.
func defaultConfigPath() string {
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "vaultix", "cli.yaml")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", "cli.yaml")
	}
	return filepath.Join(home, ".config", "vaultix", "cli.yaml")
}

// resolveConfigPath returns the --config flag when set, else the default.
func resolveConfigPath(flag string) string {
	if strings.TrimSpace(flag) != "" {
		return flag
	}
	return defaultConfigPath()
}

// loadFileConfig reads path (YAML) into a fileConfig. A missing file is not
// an error: it just means nobody has logged in yet.
func loadFileConfig(path string) (fileConfig, error) {
	var fc fileConfig
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		var nf viper.ConfigFileNotFoundError
		if errors.As(err, &nf) || os.IsNotExist(err) {
			return fc, nil
		}
		// An empty file parses as no keys; anything else unparseable is
		// a real error with the path attached.
		if isEmptyFile(path) {
			return fc, nil
		}
		return fc, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := v.Unmarshal(&fc); err != nil {
		return fc, fmt.Errorf("parse config %s: %w", path, err)
	}
	return fc, nil
}

func isEmptyFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Size() == 0
}

// resolveConfig loads the file and applies the VAULTIX_* env overrides.
func resolveConfig(path string) (*resolvedConfig, error) {
	fc, err := loadFileConfig(path)
	if err != nil {
		return nil, err
	}
	rc := &resolvedConfig{path: path, ManagerURL: fc.ManagerURL, APIKey: fc.APIKey}
	if v := strings.TrimSpace(os.Getenv(envManagerURL)); v != "" {
		rc.ManagerURL = v
	}
	if v := strings.TrimSpace(os.Getenv(envAPIKey)); v != "" {
		rc.APIKey = v
	}
	return rc, nil
}

// saveFileConfig writes fc to path as YAML, creating parent dirs (0700) and
// enforcing 0600 on the file itself — the API key is a bearer secret, so
// group/other must never read it, even if the file pre-existed looser.
func saveFileConfig(path string, fc fileConfig) error {
	data, err := encodeConfig(fc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil {
		return fmt.Errorf("write config %s: %w", path, werr)
	}
	if cerr != nil {
		return fmt.Errorf("write config %s: %w", path, cerr)
	}
	// OpenFile's mode only applies to new files; tighten pre-existing ones.
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod config %s: %w", path, err)
	}
	return nil
}

// encodeConfig renders the two scalar fields as YAML. The writer is
// deliberately minimal (quoted scalars only) so saving never depends on a
// YAML emitter; loadFileConfig (viper) parses it back.
func encodeConfig(fc fileConfig) ([]byte, error) {
	mgr, err := yamlQuote(fc.ManagerURL)
	if err != nil {
		return nil, fmt.Errorf("encode manager_url: %w", err)
	}
	key, err := yamlQuote(fc.APIKey)
	if err != nil {
		return nil, fmt.Errorf("encode api_key: %w", err)
	}
	var b strings.Builder
	b.WriteString("# Vaultix CLI config — managed by `vaultix-cli login`.\n")
	b.WriteString("# The api_key is a bearer secret: do not share this file.\n")
	b.WriteString("manager_url: " + mgr + "\n")
	b.WriteString("api_key: " + key + "\n")
	return []byte(b.String()), nil
}

// yamlQuote renders s as a YAML double-quoted scalar. Control characters
// are rejected: neither a URL nor an API key may contain them.
func yamlQuote(s string) (string, error) {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("value contains control character %U", r)
		}
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String(), nil
}
