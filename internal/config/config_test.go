package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stalane/squawk/internal/config"
)

func TestDefaultsAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")

	c, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Ntfy.BaseURL != "https://ntfy.sh" || c.Server.Addr != "127.0.0.1:8788" {
		t.Fatalf("bad defaults: %+v", c)
	}
	if c.Ntfy.Timeout != 15*time.Second || !c.Server.OpenBrowser {
		t.Fatalf("bad defaults: %+v", c)
	}

	c.Ntfy.Token = "tk_secret"
	if err := c.Save(p); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}

	got, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Ntfy.Token != "tk_secret" || got.Ntfy.Timeout != 15*time.Second {
		t.Fatalf("roundtrip: %+v", got)
	}
}

func TestMissingFileWritesDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "config.yaml")
	if _, err := config.Load(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("default config not written: %v", err)
	}
}

func TestBaseURLTrimsSlash(t *testing.T) {
	c := config.Default()
	c.Ntfy.BaseURL = "https://ntfy.sh/"
	if got := c.BaseURL(); got != "https://ntfy.sh" {
		t.Fatalf("got %q", got)
	}
}

func TestUnknownKeyRejected(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	if err := os.WriteFile(p, []byte("ntfy:\n  base_url: https://x\nbogus: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(p); err == nil {
		t.Fatal("unknown key must fail")
	}
}

func TestRedactedToken(t *testing.T) {
	c := config.Default()
	if got := c.RedactedToken(); got != "not set" {
		t.Fatalf("got %q", got)
	}
	c.Ntfy.Token = "tk_x"
	if got := c.RedactedToken(); got != "set" {
		t.Fatalf("got %q", got)
	}
}
