package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/silentfirewall/squawk/internal/cli"
	"github.com/silentfirewall/squawk/internal/version"
)

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
