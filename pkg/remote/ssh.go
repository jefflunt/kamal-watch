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

const (
	initialBackoff = 4 * time.Second
	maxBackoff     = 60 * time.Second
)

type BackoffError struct {
	ServerName string
	LastRetry  time.Time
	NextRetry  time.Time
	Failures   int
	LastErr    error
}

func (e *BackoffError) Error() string {
	return fmt.Sprintf("host offline (last retry at %s, next retry at %s after %d failed attempts: %v)",
		e.LastRetry.Format("15:04:05"),
		e.NextRetry.Format("15:04:05"),
		e.Failures,
		e.LastErr,
	)
}

func (e *BackoffError) Unwrap() error {
	return e.LastErr
}

type hostBackoffState struct {
	failures  int
	lastRetry time.Time
	nextRetry time.Time
	lastErr   error
}

type SSHPool struct {
	mu       sync.Mutex
	clients  map[string]*ssh.Client
	configs  map[string]config.ServerConfig
	backoff  map[string]*hostBackoffState
	nowFunc  func() time.Time
	dialFunc func(network, addr string, config *ssh.ClientConfig) (*ssh.Client, error)
}

func NewSSHPool(servers []config.ServerConfig) *SSHPool {
	m := make(map[string]config.ServerConfig)
	for _, s := range servers {
		m[s.Name] = s
	}
	return &SSHPool{
		clients:  make(map[string]*ssh.Client),
		configs:  m,
		backoff:  make(map[string]*hostBackoffState),
		nowFunc:  time.Now,
		dialFunc: ssh.Dial,
	}
}

func (p *SSHPool) now() time.Time {
	if p.nowFunc != nil {
		return p.nowFunc()
	}
	return time.Now()
}

func (p *SSHPool) dial(network, addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	if p.dialFunc != nil {
		return p.dialFunc(network, addr, cfg)
	}
	return ssh.Dial(network, addr, cfg)
}

func backoffDuration(failures int) time.Duration {
	if failures <= 1 {
		return initialBackoff
	}
	shift := failures - 1
	if shift > 4 {
		shift = 4
	}
	d := initialBackoff * time.Duration(1<<shift)
	if d > maxBackoff {
		return maxBackoff
	}
	return d
}

func (p *SSHPool) recordFailureLocked(serverName string, err error) {
	now := p.now()
	st, ok := p.backoff[serverName]
	if !ok {
		st = &hostBackoffState{}
		p.backoff[serverName] = st
	}
	st.failures++
	st.lastRetry = now
	st.nextRetry = now.Add(backoffDuration(st.failures))
	st.lastErr = err
}

func (p *SSHPool) clearFailureLocked(serverName string) {
	delete(p.backoff, serverName)
}

func (p *SSHPool) invalidateClientLocked(serverName string) {
	if client, ok := p.clients[serverName]; ok {
		_ = client.Close()
		delete(p.clients, serverName)
	}
}

func (p *SSHPool) invalidateClient(serverName string, target *ssh.Client) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if current, ok := p.clients[serverName]; ok && (target == nil || current == target) {
		_ = current.Close()
		delete(p.clients, serverName)
	}
}

func (p *SSHPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for name, client := range p.clients {
		_ = client.Close()
		delete(p.clients, name)
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
			p.clearFailureLocked(serverName)
			return client, nil
		}
		p.invalidateClientLocked(serverName)
	}

	now := p.now()
	if st, ok := p.backoff[serverName]; ok && now.Before(st.nextRetry) {
		return nil, &BackoffError{
			ServerName: serverName,
			LastRetry:  st.lastRetry,
			NextRetry:  st.nextRetry,
			Failures:   st.failures,
			LastErr:    st.lastErr,
		}
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
	client, err := p.dial("tcp", addr, sshConfig)
	if err != nil {
		dialErr := fmt.Errorf("ssh dial %s: %w", addr, err)
		p.recordFailureLocked(serverName, dialErr)
		return nil, dialErr
	}

	p.clearFailureLocked(serverName)
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
		p.invalidateClient(serverName, client)

		client, err = p.GetClient(serverName)
		if err != nil {
			return nil, err
		}
		session, err = client.NewSession()
		if err != nil {
			p.invalidateClient(serverName, client)
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
		_ = session.Close()
		if ctx.Err() == context.DeadlineExceeded {
			p.invalidateClient(serverName, client)
		}
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
		p.invalidateClient(serverName, client)

		client, err = p.GetClient(serverName)
		if err != nil {
			return err
		}
		session, err = client.NewSession()
		if err != nil {
			p.invalidateClient(serverName, client)
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
		_ = session.Close()
		return ctx.Err()
	case err := <-done:
		return err
	}
}
