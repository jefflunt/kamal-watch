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
	if cfg.Servers[0].InsecureIgnoreHostKey {
		t.Errorf("expected InsecureIgnoreHostKey to default to false")
	}
	if cfg.Servers[0].KnownHostsPath == "" {
		t.Errorf("expected KnownHostsPath to default to ~/.ssh/known_hosts")
	}
}

func TestLoadConfig_HostKeyOptions(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "test_hostkey.yml")
	content := []byte(`
known_hosts_path: /etc/ssh/ssh_known_hosts
insecure_ignore_host_key: true
servers:
  - name: inherit-server
    host: 10.0.0.1
  - name: custom-server
    host: 10.0.0.2
    known_hosts_path: /custom/known_hosts
`)
	if err := os.WriteFile(configFile, content, 0644); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}

	cfg, err := LoadConfig(configFile)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}
	if len(cfg.Servers) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(cfg.Servers))
	}
	if cfg.Servers[0].KnownHostsPath != "/etc/ssh/ssh_known_hosts" {
		t.Errorf("expected inherit-server to inherit /etc/ssh/ssh_known_hosts, got %s", cfg.Servers[0].KnownHostsPath)
	}
	if !cfg.Servers[0].InsecureIgnoreHostKey {
		t.Errorf("expected inherit-server to inherit insecure_ignore_host_key=true")
	}
	if cfg.Servers[1].KnownHostsPath != "/custom/known_hosts" {
		t.Errorf("expected custom-server to use /custom/known_hosts, got %s", cfg.Servers[1].KnownHostsPath)
	}
}
