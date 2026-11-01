package config

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

type ServerConfig struct {
	Name     string `yaml:"name"`
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	KeyPath  string `yaml:"key_path"`
}

type Config struct {
	ListenAddr   string         `yaml:"listen_addr"`
	PollInterval time.Duration  `yaml:"poll_interval"`
	Servers      []ServerConfig `yaml:"servers"`
}

func defaultSSHUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "root"
}

func defaultSSHKeyPath() string {
	home, _ := os.UserHomeDir()
	ed25519Key := filepath.Join(home, ".ssh", "id_ed25519")
	if _, err := os.Stat(ed25519Key); err == nil {
		return ed25519Key
	}
	rsaKey := filepath.Join(home, ".ssh", "id_rsa")
	if _, err := os.Stat(rsaKey); err == nil {
		return rsaKey
	}
	return ed25519Key
}

func DefaultConfig() *Config {
	return &Config{
		ListenAddr:   ":8080",
		PollInterval: 4 * time.Second,
		Servers: []ServerConfig{
			{
				Name:    "localhost",
				Host:    "localhost",
				Port:    22,
				User:    defaultSSHUser(),
				KeyPath: defaultSSHKeyPath(),
			},
		},
	}
}

func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	for i := range cfg.Servers {
		if cfg.Servers[i].Port == 0 {
			cfg.Servers[i].Port = 22
		}
		if cfg.Servers[i].User == "" {
			cfg.Servers[i].User = defaultSSHUser()
		}
		if cfg.Servers[i].KeyPath == "" {
			cfg.Servers[i].KeyPath = defaultSSHKeyPath()
		} else if cfg.Servers[i].KeyPath[0] == '~' {
			home, _ := os.UserHomeDir()
			cfg.Servers[i].KeyPath = filepath.Join(home, cfg.Servers[i].KeyPath[1:])
		}
	}

	return cfg, nil
}
