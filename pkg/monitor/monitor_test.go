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
	"github.com/jefflunt/kamal-watch/pkg/models"
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

func TestParseHostUpdates(t *testing.T) {
	tests := []struct {
		name               string
		input              string
		wantReboot         bool
		wantPkgs           []string
		wantUpdates        int
		wantSecurity       int
		wantSummaryContain string
	}{
		{
			name: "debian reboot required with packages and pending security updates",
			input: `REBOOT_REQUIRED:1
REBOOT_PKGS_START
linux-image-6.8.0-45-generic
libc6
linux-image-6.8.0-45-generic
REBOOT_PKGS_END
UPDATES_START
12 updates can be applied immediately.
3 of these updates are standard security updates.
To see these additional updates run: apt list --upgradable
UPDATES_END`,
			wantReboot:         true,
			wantPkgs:           []string{"linux-image-6.8.0-45-generic", "libc6"},
			wantUpdates:        12,
			wantSecurity:       3,
			wantSummaryContain: "12 updates can be applied immediately.",
		},
		{
			name: "ubuntu older format packages can be updated",
			input: `REBOOT_REQUIRED:0
REBOOT_PKGS_START
REBOOT_PKGS_END
UPDATES_START
5 packages can be updated.
1 update is a security update.
UPDATES_END`,
			wantReboot:         false,
			wantPkgs:           nil,
			wantUpdates:        5,
			wantSecurity:       1,
			wantSummaryContain: "5 packages can be updated.",
		},
		{
			name: "clean host with no reboot and zero updates",
			input: `REBOOT_REQUIRED:0
REBOOT_PKGS_START
REBOOT_PKGS_END
UPDATES_START
0 updates can be applied immediately.
UPDATES_END`,
			wantReboot:         false,
			wantPkgs:           nil,
			wantUpdates:        0,
			wantSecurity:       0,
			wantSummaryContain: "0 updates can be applied immediately.",
		},
		{
			name: "rhel needs-restarting reboot required without apt notifier",
			input: `REBOOT_REQUIRED:1
REBOOT_PKGS_START
REBOOT_PKGS_END
UPDATES_START
UPDATES_END`,
			wantReboot:         true,
			wantPkgs:           nil,
			wantUpdates:        0,
			wantSecurity:       0,
			wantSummaryContain: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var h models.HostHealth
			parseHostUpdates(tc.input, &h)

			if h.RebootRequired != tc.wantReboot {
				t.Errorf("RebootRequired = %v; want %v", h.RebootRequired, tc.wantReboot)
			}
			if len(h.RebootRequiredPkgs) != len(tc.wantPkgs) {
				t.Fatalf("RebootRequiredPkgs = %v; want %v", h.RebootRequiredPkgs, tc.wantPkgs)
			}
			for i := range tc.wantPkgs {
				if h.RebootRequiredPkgs[i] != tc.wantPkgs[i] {
					t.Errorf("RebootRequiredPkgs[%d] = %q; want %q", i, h.RebootRequiredPkgs[i], tc.wantPkgs[i])
				}
			}
			if h.UpdatesAvailable != tc.wantUpdates {
				t.Errorf("UpdatesAvailable = %d; want %d", h.UpdatesAvailable, tc.wantUpdates)
			}
			if h.SecurityUpdates != tc.wantSecurity {
				t.Errorf("SecurityUpdates = %d; want %d", h.SecurityUpdates, tc.wantSecurity)
			}
			if tc.wantSummaryContain != "" && !strings.Contains(h.UpdatesSummary, tc.wantSummaryContain) {
				t.Errorf("UpdatesSummary = %q; want substring %q", h.UpdatesSummary, tc.wantSummaryContain)
			}
		})
	}
}
