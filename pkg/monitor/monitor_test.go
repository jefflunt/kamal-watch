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

func TestCleanCPUModel(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{
			input: "model name : AMD Ryzen 9 5950X 16-Core Processor",
			want:  "AMD 5950X",
		},
		{
			input: "model name : AMD Ryzen 7 5800X 8-Core Processor",
			want:  "AMD 5800X",
		},
		{
			input: "model name : Intel(R) Core(TM) i9-10900K CPU @ 3.70GHz",
			want:  "Intel i9-10900K",
		},
		{
			input: "model name : Intel(R) Xeon(R) Gold 6248R CPU @ 3.00GHz",
			want:  "Intel Xeon Gold 6248R",
		},
		{
			input: "model name : Intel(R) Xeon(R) CPU E5-2680 v4 @ 2.40GHz",
			want:  "Intel Xeon E5-2680 v4",
		},
		{
			input: "AMD EPYC 7763 64-Core Processor",
			want:  "AMD EPYC 7763",
		},
		{
			input: "Apple M1 Max",
			want:  "Apple M1 Max",
		},
		{
			input: "ARM Neoverse-N1",
			want:  "ARM Neoverse-N1",
		},
		{
			input: "",
			want:  "CPU",
		},
	}

	for _, tc := range tests {
		got := CleanCPUModel(tc.input)
		if got != tc.want {
			t.Errorf("CleanCPUModel(%q) = %q; want %q", tc.input, got, tc.want)
		}
	}
}

func TestCPULabel(t *testing.T) {
	tests := []struct {
		health models.HostHealth
		want   string
	}{
		{
			health: models.HostHealth{
				CPUSockets: 1,
				CPUCores:   32,
				CPUModel:   "AMD 5950X",
			},
			want: "1x32 [AMD 5950X]",
		},
		{
			health: models.HostHealth{
				CPUSockets: 2,
				CPUCores:   64,
				CPUModel:   "AMD EPYC 7763",
			},
			want: "2x64 [AMD EPYC 7763]",
		},
		{
			health: models.HostHealth{
				CPUSockets: 0,
				CPUCores:   8,
				CPUModel:   "",
			},
			want: "1x8 [CPU]",
		},
		{
			health: models.HostHealth{
				CPUSockets:    1,
				CPUCores:      0,
				CPUCoresUsage: []float64{10.0, 20.0, 30.0, 40.0},
				CPUModel:      "Intel i7-12700K",
			},
			want: "1x4 [Intel i7-12700K]",
		},
	}

	for _, tc := range tests {
		got := tc.health.CPULabel()
		if got != tc.want {
			t.Errorf("CPULabel() = %q; want %q", got, tc.want)
		}
	}
}

func TestParseProcStat_Deltas(t *testing.T) {
	mon := NewMonitor(&config.Config{}, nil)
	server := "srv-1"

	// First sample
	sample1 := `cpu  1000 0 1000 8000 0 0 0 0 0 0
cpu0 500 0 500 4000 0 0 0 0 0 0
cpu1 500 0 500 4000 0 0 0 0 0 0`

	usages1 := mon.parseProcStat(server, sample1)
	if len(usages1) != 2 {
		t.Fatalf("expected 2 cores in usages1, got %d", len(usages1))
	}
	// Initial boot usage: (500+500)/(500+500+4000) = 1000/5000 = 20%
	if usages1[0] < 19.9 || usages1[0] > 20.1 {
		t.Errorf("expected ~20%% usage on core 0, got %.2f%%", usages1[0])
	}

	// Second sample with delta:
	// core 0: busy increases by 50, idle increases by 50 -> 50%
	// core 1: busy increases by 10, idle increases by 90 -> 10%
	sample2 := `cpu  1060 0 1060 8140 0 0 0 0 0 0
cpu0 525 0 525 4050 0 0 0 0 0 0
cpu1 505 0 505 4090 0 0 0 0 0 0`

	usages2 := mon.parseProcStat(server, sample2)
	if len(usages2) != 2 {
		t.Fatalf("expected 2 cores in usages2, got %d", len(usages2))
	}
	if usages2[0] < 49.9 || usages2[0] > 50.1 {
		t.Errorf("expected ~50%% usage on core 0, got %.2f%%", usages2[0])
	}
	if usages2[1] < 9.9 || usages2[1] > 10.1 {
		t.Errorf("expected ~10%% usage on core 1, got %.2f%%", usages2[1])
	}
}
