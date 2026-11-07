package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/silentfirewall/squawk/internal/version"
)

const usage = `squawk — send-only ntfy alert publisher

Usage:
  squawk                         serve the UI and open the browser
  squawk serve [--addr ADDR]     serve the UI without opening a browser
  squawk open                    open the running UI
  squawk send --audience LABEL --severity urgent|warning|info \
              --headline TEXT --details TEXT [--link URL] [--dry-run]
  squawk test [--audience LABEL]
  squawk topics list|add|remove
  squawk config path|show
  squawk service install|uninstall|status
  squawk version

Global flags:
  --config PATH    config file (default: OS config dir /squawk/config.yaml)
  --data-dir PATH  override the config and database directory
`

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return 0
	}

	switch args[0] {
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "squawk %s\n", version.Version)
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "squawk: unknown command %q\n\n", args[0])
		fmt.Fprint(stderr, usage)
		return 2
	}
}
