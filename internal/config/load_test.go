package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAppliesDefaultStartupTimeout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"listen_addr":":8080","state_dir":"/state","models_dir":"/models","llama_server_bin":"/usr/local/bin/llama-server"}`), 0o644); err != nil {
		t.Fatalf("WriteFile() unexpected error: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.StartupTimeoutSeconds != defaultStartupTimeoutSeconds {
		t.Fatalf("StartupTimeoutSeconds = %d, want %d", cfg.StartupTimeoutSeconds, defaultStartupTimeoutSeconds)
	}
}

func TestLoadRespectsExplicitStartupTimeout(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"listen_addr":":8080","state_dir":"/state","models_dir":"/models","llama_server_bin":"/usr/local/bin/llama-server","startup_timeout_seconds":75}`), 0o644); err != nil {
		t.Fatalf("WriteFile() unexpected error: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.StartupTimeoutSeconds != 75 {
		t.Fatalf("StartupTimeoutSeconds = %d, want 75", cfg.StartupTimeoutSeconds)
	}
}
