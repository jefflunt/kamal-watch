package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.ListenAddr != ":8080" {
		t.Errorf("expected :8080, got %s", cfg.ListenAddr)
	}
	if cfg.PollInterval != 4*time.Second {
		t.Errorf("expected 4s poll interval, got %v", cfg.PollInterval)
	}
	if len(cfg.Servers) == 0 {
		t.Errorf("expected default servers to not be empty")
	}
}

func TestLoadConfig_NonExistent(t *testing.T) {
	cfg, err := LoadConfig("/path/to/nonexistent/config.yml")
	if err != nil {
		t.Fatalf("unexpected error loading non-existent config: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected default config returned for non-existent file")
	}
}

func TestLoadConfig_Valid(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "test.yml")
	content := []byte(`
listen_addr: ":9090"
poll_interval: 5s
servers:
  - name: test-server
    host: 203.0.113.10
    port: 22
    user: testuser
    key_path: /tmp/key
`)
	if err := os.WriteFile(configFile, content, 0644); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}

	cfg, err := LoadConfig(configFile)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if cfg.ListenAddr != ":9090" {
		t.Errorf("expected :9090, got %s", cfg.ListenAddr)
	}
	if cfg.PollInterval != 5*time.Second {
		t.Errorf("expected 5s, got %v", cfg.PollInterval)
	}
	if len(cfg.Servers) != 1 || cfg.Servers[0].Name != "test-server" {
		t.Errorf("unexpected servers config: %+v", cfg.Servers)
	}
}
