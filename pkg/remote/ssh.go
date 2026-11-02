package remote

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jefflunt/kamal-watch/config"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type SSHPool struct {
	mu      sync.Mutex
	clients map[string]*ssh.Client
	configs map[string]config.ServerConfig
}

func NewSSHPool(servers []config.ServerConfig) *SSHPool {
	m := make(map[string]config.ServerConfig)
	for _, s := range servers {
		m[s.Name] = s
	}
	return &SSHPool{
		clients: make(map[string]*ssh.Client),
		configs: m,
	}
}

func hostKeyCallbackForServer(cfg config.ServerConfig) (ssh.HostKeyCallback, error) {
	if cfg.InsecureIgnoreHostKey {
		return ssh.InsecureIgnoreHostKey(), nil
	}

	knownHostsPath := cfg.KnownHostsPath
	if knownHostsPath == "" {
		home, _ := os.UserHomeDir()
		knownHostsPath = filepath.Join(home, ".ssh", "known_hosts")
	} else if knownHostsPath[0] == '~' {
		home, _ := os.UserHomeDir()
		knownHostsPath = filepath.Join(home, knownHostsPath[1:])
	}

	cb, err := knownhosts.New(knownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("load known_hosts %s (or set insecure_ignore_host_key: true): %w", knownHostsPath, err)
	}
	return cb, nil
}

func (p *SSHPool) GetClient(serverName string) (*ssh.Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	cfg, exists := p.configs[serverName]
	if !exists {
		return nil, fmt.Errorf("unknown server: %s", serverName)
	}

	if client, ok := p.clients[serverName]; ok {
		// Test connection with a keepalive
		_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
		if err == nil {
			return client, nil
		}
		_ = client.Close()
		delete(p.clients, serverName)
	}

	keyPath := cfg.KeyPath
	if keyPath != "" && keyPath[0] == '~' {
		home, _ := os.UserHomeDir()
		keyPath = filepath.Join(home, keyPath[1:])
	}

	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read private key %s: %w", keyPath, err)
	}

	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key %s: %w", keyPath, err)
	}

	hostKeyCallback, err := hostKeyCallbackForServer(cfg)
	if err != nil {
		return nil, err
	}

	sshConfig := &ssh.ClientConfig{
		User: cfg.User,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: hostKeyCallback,
		Timeout:         6 * time.Second,
	}

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	client, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s: %w", addr, err)
	}

	p.clients[serverName] = client
	return client, nil
}

func (p *SSHPool) RunCommand(ctx context.Context, serverName, cmd string) ([]byte, error) {
	client, err := p.GetClient(serverName)
	if err != nil {
		return nil, err
	}

	session, err := client.NewSession()
	if err != nil {
		p.mu.Lock()
		delete(p.clients, serverName)
		p.mu.Unlock()

		client, err = p.GetClient(serverName)
		if err != nil {
			return nil, err
		}
		session, err = client.NewSession()
		if err != nil {
			return nil, fmt.Errorf("create session: %w", err)
		}
	}
	defer session.Close()

	type result struct {
		out []byte
		err error
	}
	resChan := make(chan result, 1)

	go func() {
		out, err := session.CombinedOutput(cmd)
		resChan <- result{out: out, err: err}
	}()

	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		return nil, ctx.Err()
	case res := <-resChan:
		return res.out, res.err
	}
}

func (p *SSHPool) StreamCommand(ctx context.Context, serverName, cmd string, out io.Writer) error {
	client, err := p.GetClient(serverName)
	if err != nil {
		return err
	}

	session, err := client.NewSession()
	if err != nil {
		p.mu.Lock()
		delete(p.clients, serverName)
		p.mu.Unlock()

		client, err = p.GetClient(serverName)
		if err != nil {
			return err
		}
		session, err = client.NewSession()
		if err != nil {
			return fmt.Errorf("create session: %w", err)
		}
	}
	defer session.Close()

	session.Stdout = out
	session.Stderr = out

	if err := session.Start(cmd); err != nil {
		return fmt.Errorf("start stream: %w", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- session.Wait()
	}()

	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGINT)
		time.Sleep(100 * time.Millisecond)
		_ = session.Signal(ssh.SIGKILL)
		return ctx.Err()
	case err := <-done:
		return err
	}
}
