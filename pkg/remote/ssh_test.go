package remote

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
