package remote

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jefflunt/kamal-watch/config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

func generateTestPublicKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("new ssh public key: %v", err)
	}
	return sshPub
}

func TestHostKeyCallbackForServer_VerifiedKnownHosts(t *testing.T) {
	tmpDir := t.TempDir()
	knownHostsFile := filepath.Join(tmpDir, "known_hosts")

	trustedKey := generateTestPublicKey(t)
	untrustedKey := generateTestPublicKey(t)

	line := knownhosts.Line([]string{"10.0.1.10:22"}, trustedKey)
	if err := os.WriteFile(knownHostsFile, []byte(line+"\n"), 0600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}

	srvCfg := config.ServerConfig{
		Name:                  "app-server-01",
		Host:                  "10.0.1.10",
		Port:                  22,
		KnownHostsPath:        knownHostsFile,
		InsecureIgnoreHostKey: false,
	}

	cb, err := hostKeyCallbackForServer(srvCfg)
	if err != nil {
		t.Fatalf("unexpected error building callback: %v", err)
	}

	remoteAddr := &net.TCPAddr{IP: net.ParseIP("10.0.1.10"), Port: 22}

	// Trusted host key should pass verification
	if err := cb("10.0.1.10:22", remoteAddr, trustedKey); err != nil {
		t.Errorf("expected trusted host key to pass verification, got: %v", err)
	}

	// Untrusted host key (MITM) should fail verification
	if err := cb("10.0.1.10:22", remoteAddr, untrustedKey); err == nil {
		t.Errorf("expected untrusted host key to be rejected")
	}

	// Unknown host should fail verification
	otherAddr := &net.TCPAddr{IP: net.ParseIP("10.0.1.99"), Port: 22}
	if err := cb("10.0.1.99:22", otherAddr, trustedKey); err == nil {
		t.Errorf("expected unknown host to be rejected")
	}
}

func TestHostKeyCallbackForServer_MissingKnownHostsFailsByDefault(t *testing.T) {
	srvCfg := config.ServerConfig{
		Name:                  "app-server-01",
		Host:                  "10.0.1.10",
		Port:                  22,
		KnownHostsPath:        "/nonexistent/path/known_hosts",
		InsecureIgnoreHostKey: false,
	}

	_, err := hostKeyCallbackForServer(srvCfg)
	if err == nil {
		t.Fatalf("expected error when known_hosts file does not exist and InsecureIgnoreHostKey is false")
	}
	if !strings.Contains(err.Error(), "known_hosts") {
		t.Errorf("expected error to mention known_hosts, got: %v", err)
	}
}

func TestHostKeyCallbackForServer_InsecureOptIn(t *testing.T) {
	srvCfg := config.ServerConfig{
		Name:                  "test-box",
		Host:                  "10.0.1.10",
		Port:                  22,
		KnownHostsPath:        "/nonexistent/path/known_hosts",
		InsecureIgnoreHostKey: true,
	}

	cb, err := hostKeyCallbackForServer(srvCfg)
	if err != nil {
		t.Fatalf("expected no error when InsecureIgnoreHostKey is true, got: %v", err)
	}

	randomKey := generateTestPublicKey(t)
	remoteAddr := &net.TCPAddr{IP: net.ParseIP("10.0.1.10"), Port: 22}
	if err := cb(fmt.Sprintf("%s:%d", srvCfg.Host, srvCfg.Port), remoteAddr, randomKey); err != nil {
		t.Errorf("expected InsecureIgnoreHostKey callback to accept arbitrary key, got: %v", err)
	}
}

func writeTestPrivateKey(t *testing.T, dir string) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	pemBlock, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	keyFile := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(pemBlock), 0600); err != nil {
		t.Fatalf("write private key: %v", err)
	}
	return keyFile
}

func TestBackoffDuration(t *testing.T) {
	expected := map[int]time.Duration{
		1: 4 * time.Second,
		2: 8 * time.Second,
		3: 16 * time.Second,
		4: 32 * time.Second,
		5: 60 * time.Second,
		6: 60 * time.Second,
	}
	for failures, want := range expected {
		if got := backoffDuration(failures); got != want {
			t.Errorf("backoffDuration(%d) = %v, want %v", failures, got, want)
		}
	}
}

func TestSSHPool_ExponentialBackoffAndCooldown(t *testing.T) {
	tmpDir := t.TempDir()
	keyPath := writeTestPrivateKey(t, tmpDir)

	srv := config.ServerConfig{
		Name:                  "web-01",
		Host:                  "10.0.1.10",
		Port:                  22,
		User:                  "deploy",
		KeyPath:               keyPath,
		InsecureIgnoreHostKey: true,
	}

	pool := NewSSHPool([]config.ServerConfig{srv})
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	pool.nowFunc = func() time.Time { return now }

	dialAttempts := 0
	shouldSucceed := false
	pool.dialFunc = func(network, addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
		dialAttempts++
		if !shouldSucceed {
			return nil, errors.New("connection refused")
		}
		return &ssh.Client{}, nil
	}

	// 1st attempt fails -> triggers 4s backoff
	_, err := pool.GetClient("web-01")
	if err == nil {
		t.Fatalf("expected 1st dial to fail")
	}
	if dialAttempts != 1 {
		t.Fatalf("expected 1 dial attempt, got %d", dialAttempts)
	}

	// 2s later (within 4s cooldown) -> should NOT dial and should return *BackoffError
	now = now.Add(2 * time.Second)
	_, err = pool.GetClient("web-01")
	var backoffErr *BackoffError
	if !errors.As(err, &backoffErr) {
		t.Fatalf("expected *BackoffError during cooldown, got: %v", err)
	}
	if dialAttempts != 1 {
		t.Fatalf("expected dial to be skipped during cooldown, got %d attempts", dialAttempts)
	}
	if backoffErr.Failures != 1 {
		t.Errorf("expected Failures=1, got %d", backoffErr.Failures)
	}

	// Advance past 4s cooldown -> 2nd dial attempted and fails -> triggers 8s backoff
	now = now.Add(3 * time.Second)
	_, err = pool.GetClient("web-01")
	if err == nil || errors.As(err, &backoffErr) {
		t.Fatalf("expected fresh dial failure after cooldown, got: %v", err)
	}
	if dialAttempts != 2 {
		t.Fatalf("expected 2 dial attempts, got %d", dialAttempts)
	}

	// 5s later (still within 8s cooldown) -> skipped
	now = now.Add(5 * time.Second)
	_, err = pool.GetClient("web-01")
	if !errors.As(err, &backoffErr) {
		t.Fatalf("expected *BackoffError during 8s cooldown, got: %v", err)
	}
	if dialAttempts != 2 {
		t.Fatalf("expected dial to be skipped during 8s cooldown, got %d attempts", dialAttempts)
	}

	// Advance past 8s cooldown and allow dial to succeed -> backoff state cleared
	now = now.Add(4 * time.Second)
	shouldSucceed = true
	_, err = pool.GetClient("web-01")
	if err != nil {
		t.Fatalf("expected dial to succeed after recovery, got: %v", err)
	}
	if dialAttempts != 3 {
		t.Fatalf("expected 3 dial attempts, got %d", dialAttempts)
	}
	if _, exists := pool.backoff["web-01"]; exists {
		t.Errorf("expected backoff state to be cleared after successful dial")
	}
}
