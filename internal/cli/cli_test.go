package cli_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silentfirewall/squawk/internal/cli"
	"github.com/silentfirewall/squawk/internal/store"
	"github.com/silentfirewall/squawk/internal/version"
)

func seedAudience(t *testing.T, dir string) {
	t.Helper()
	st, err := store.Open(filepath.Join(dir, "squawk.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.CreateAudience(context.Background(), store.Audience{
		Label: "Family", Topic: "squawk-family-abc", DefaultSeverity: "urgent",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestVersionCommand(t *testing.T) {
	var out bytes.Buffer
	code := cli.Run(context.Background(), []string{"version"}, &out, &out)
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(out.String(), version.Version) {
		t.Fatalf("output %q does not contain %q", out.String(), version.Version)
	}
}

func TestUnknownCommand(t *testing.T) {
	var out bytes.Buffer
	code := cli.Run(context.Background(), []string{"bogus"}, &out, &out)
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
}

func TestSendDryRun(t *testing.T) {
	dir := t.TempDir()
	seedAudience(t, dir)

	var out bytes.Buffer
	code := cli.Run(context.Background(), []string{
		"send", "--data-dir", dir,
		"--audience", "Family", "--severity", "urgent",
		"--headline", "H", "--details", "D", "--dry-run",
	}, &out, &out)
	if code != 0 {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
	if !strings.Contains(out.String(), "🚨 H -- D") {
		t.Fatalf("out=%q", out.String())
	}
}

func TestSendUnknownAudience(t *testing.T) {
	dir := t.TempDir()
	seedAudience(t, dir)

	var out bytes.Buffer
	code := cli.Run(context.Background(), []string{
		"send", "--data-dir", dir,
		"--audience", "Nope", "--severity", "info",
		"--headline", "H", "--details", "D", "--dry-run",
	}, &out, &out)
	if code == 0 {
		t.Fatal("unknown audience must fail")
	}
}

func TestInvalidSeverity(t *testing.T) {
	dir := t.TempDir()
	seedAudience(t, dir)

	var out bytes.Buffer
	code := cli.Run(context.Background(), []string{
		"send", "--data-dir", dir,
		"--audience", "Family", "--severity", "critical",
		"--headline", "H", "--details", "D", "--dry-run",
	}, &out, &out)
	if code == 0 {
		t.Fatal("invalid severity must fail")
	}
}

func TestTopicsList(t *testing.T) {
	dir := t.TempDir()
	seedAudience(t, dir)

	var out bytes.Buffer
	code := cli.Run(context.Background(), []string{"topics", "list", "--data-dir", dir}, &out, &out)
	if code != 0 || !strings.Contains(out.String(), "squawk-family-abc") {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
}

func TestConfigShowRedactsToken(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	code := cli.Run(context.Background(), []string{"config", "show", "--data-dir", dir}, &out, &out)
	if code != 0 {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
	if strings.Contains(out.String(), "token: ") && !strings.Contains(out.String(), "token: not set") {
		t.Fatalf("token value leaked: %q", out.String())
	}
}
