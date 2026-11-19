package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vaultix", "cli.yaml")
	want := fileConfig{ManagerURL: "https://manager.example:8443", APIKey: "vxk_test_key_123"}
	if err := saveFileConfig(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := loadFileConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch: got %+v want %+v", got, want)
	}
}

func TestConfigFilePerms0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli.yaml")
	fc := fileConfig{ManagerURL: "https://m:8443", APIKey: "vxk_secret"}
	if err := saveFileConfig(path, fc); err != nil {
		t.Fatalf("save: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("config perms = %o, want 600", perm)
	}
	// Saving over a pre-existing looser file must tighten it.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := saveFileConfig(path, fc); err != nil {
		t.Fatalf("resave: %v", err)
	}
	st, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Fatalf("resaved perms = %o, want 600", perm)
	}
}

func TestConfigDirPerms0700(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vaultix")
	path := filepath.Join(dir, "cli.yaml")
	if err := saveFileConfig(path, fileConfig{ManagerURL: "https://m:8443"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o700 {
		t.Fatalf("config dir perms = %o, want 700", perm)
	}
}

func TestConfigMissingFileIsEmpty(t *testing.T) {
	got, err := loadFileConfig(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if got != (fileConfig{}) {
		t.Fatalf("missing file should load empty, got %+v", got)
	}
}

func TestConfigEnvOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli.yaml")
	if err := saveFileConfig(path, fileConfig{ManagerURL: "https://file:8443", APIKey: "vxk_file"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envManagerURL, "https://env:8443")
	t.Setenv(envAPIKey, "vxk_env")
	rc, err := resolveConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if rc.ManagerURL != "https://env:8443" || rc.APIKey != "vxk_env" {
		t.Fatalf("env override failed: %+v", rc)
	}
}

func TestConfigEnvPartialOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli.yaml")
	if err := saveFileConfig(path, fileConfig{ManagerURL: "https://file:8443", APIKey: "vxk_file"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envAPIKey, "vxk_env")
	rc, err := resolveConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if rc.ManagerURL != "https://file:8443" {
		t.Fatalf("file manager URL should survive: %+v", rc)
	}
	if rc.APIKey != "vxk_env" {
		t.Fatalf("env key should win: %+v", rc)
	}
}

func TestConfigValuesWithQuotesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli.yaml")
	want := fileConfig{ManagerURL: `https://m:8443/"quoted"`, APIKey: `vxk_\with"both`}
	if err := saveFileConfig(path, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := loadFileConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got != want {
		t.Fatalf("round trip mismatch: got %+v want %+v", got, want)
	}
}

func TestConfigRejectsControlCharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cli.yaml")
	err := saveFileConfig(path, fileConfig{ManagerURL: "https://m:8443", APIKey: "vxk_bad\nkey"})
	if err == nil || !strings.Contains(err.Error(), "control character") {
		t.Fatalf("expected control-character error, got %v", err)
	}
}

func TestResolveConfigPath(t *testing.T) {
	if got := resolveConfigPath("--custom/path.yaml"); got != "--custom/path.yaml" {
		t.Fatalf("--config flag not honored: %q", got)
	}
	def := resolveConfigPath("")
	if !strings.HasSuffix(def, filepath.Join("vaultix", "cli.yaml")) {
		t.Fatalf("default path %q should end in vaultix/cli.yaml", def)
	}
}
