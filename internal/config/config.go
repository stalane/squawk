package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Ntfy   Ntfy   `yaml:"ntfy"`
	Server Server `yaml:"server"`
}

type Ntfy struct {
	BaseURL string        `yaml:"base_url"`
	Token   string        `yaml:"token"`
	Timeout time.Duration `yaml:"timeout"`
}

type Server struct {
	Addr        string `yaml:"addr"`
	OpenBrowser bool   `yaml:"open_browser"`
}

func Default() *Config {
	return &Config{
		Ntfy: Ntfy{
			BaseURL: "https://ntfy.sh",
			Timeout: 15 * time.Second,
		},
		Server: Server{
			Addr:        "127.0.0.1:8788",
			OpenBrowser: true,
		},
	}
}

func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "squawk", "config.yaml"), nil
}

func Load(path string) (*Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := cfg.Save(path); err != nil {
			return nil, err
		}
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Ntfy.Timeout <= 0 {
		cfg.Ntfy.Timeout = 15 * time.Second
	}
	return cfg, nil
}

func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".config-*.yaml")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func (c *Config) BaseURL() string {
	return strings.TrimRight(c.Ntfy.BaseURL, "/")
}

func (c *Config) RedactedToken() string {
	if c.Ntfy.Token == "" {
		return "not set"
	}
	return "set"
}
