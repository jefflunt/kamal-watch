package monitor

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jefflunt/kamal-watch/config"
	"github.com/jefflunt/kamal-watch/pkg/remote"
	"golang.org/x/crypto/ssh"
)

func TestParseBytes(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
	}{
		{"", 0},
		{"-", 0},
		{"100B", 100},
		{"500KiB", 500 * 1024},
		{"500kB", 500 * 1024},
		{"160.8MiB", 168611020},
		{"1.5GiB", 1610612736},
	}

	for _, tt := range tests {
		got := parseBytes(tt.input)
		// Allow small float rounding delta
		diff := got - tt.expected
		if diff < -1024 || diff > 1024 {
			t.Errorf("parseBytes(%q) = %d; expected %d", tt.input, got, tt.expected)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "-"},
		{-1, "-"},
		{500, "500B"},
		{1024 * 500, "500.0KiB"},
		{1024 * 1024 * 160, "160.0MiB"},
		{int64(1024 * 1024 * 1024 * 2.5), "2.50GiB"},
	}

	for _, tt := range tests {
		got := formatBytes(tt.input)
		if got != tt.expected {
			t.Errorf("formatBytes(%d) = %q; expected %q", tt.input, got, tt.expected)
		}
	}
}

func TestPollServer_BackoffCooldownMarksOfflineWithLastRetry(t *testing.T) {
	tmpDir := t.TempDir()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pemBlock, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyFile := filepath.Join(tmpDir, "id_ed25519")
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(pemBlock), 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	srv := config.ServerConfig{
		Name:                  "offline-node",
		Host:                  "127.0.0.1",
		Port:                  1, // closed port
		User:                  "deploy",
		KeyPath:               keyFile,
		InsecureIgnoreHostKey: true,
	}
	cfg := &config.Config{
		PollInterval: 4 * time.Second,
		Servers:      []config.ServerConfig{srv},
	}
	pool := remote.NewSSHPool(cfg.Servers)
	mon := NewMonitor(cfg, pool)

	ctx := context.Background()
	inv1 := mon.PollServer(ctx, srv)
	if inv1.Online {
		t.Fatalf("expected inv1 to be offline")
	}

	// Immediate second poll should hit backoff cooldown without dialing
	inv2 := mon.PollServer(ctx, srv)
	if inv2.Online {
		t.Fatalf("expected inv2 to be offline")
	}
	if !strings.Contains(inv2.Error, "host offline (last retry at") {
		t.Errorf("expected backoff error message in inv2.Error, got: %s", inv2.Error)
	}
}
