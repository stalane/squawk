package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/silentfirewall/squawk/internal/assets"
	"github.com/silentfirewall/squawk/internal/config"
	"github.com/silentfirewall/squawk/internal/format"
	"github.com/silentfirewall/squawk/internal/ntfy"
	"github.com/silentfirewall/squawk/internal/service"
	"github.com/silentfirewall/squawk/internal/store"
	"github.com/silentfirewall/squawk/internal/version"
	"github.com/silentfirewall/squawk/internal/web"
)

const usage = `squawk — send-only ntfy alert publisher

Usage:
  squawk                         serve the UI and open the browser
  squawk serve [--addr ADDR]     serve the UI without opening a browser
  squawk open                    open the running UI
  squawk send --audience LABEL --severity urgent|warning|info \
              --headline TEXT --details TEXT [--link URL] [--dry-run]
  squawk test [--audience LABEL]
  squawk topics list|add <label> [topic]|remove <label>
  squawk config path|show
  squawk service install|uninstall|status
  squawk version

Global flags:
  --config PATH    config file (default: OS config dir /squawk/config.yaml)
  --data-dir PATH  override the config and database directory
`

type globals struct {
	configPath   string
	dataDir      string
	addrOverride string
}

func (g globals) paths() (cfgPath, dbPath string, err error) {
	if g.dataDir != "" {
		return filepath.Join(g.dataDir, "config.yaml"), filepath.Join(g.dataDir, "squawk.db"), nil
	}
	cfgPath = g.configPath
	if cfgPath == "" {
		cfgPath, err = config.Path()
		if err != nil {
			return "", "", err
		}
	}
	return cfgPath, filepath.Join(filepath.Dir(cfgPath), "squawk.db"), nil
}

type livePublisher struct {
	cfg *config.Config
}

func (p livePublisher) Publish(ctx context.Context, topic string, m ntfy.Message) (ntfy.Result, error) {
	client := ntfy.New(ntfy.Config{
		BaseURL:   p.cfg.BaseURL(),
		Token:     p.cfg.Ntfy.Token,
		UserAgent: "squawk/" + version.Version,
		Timeout:   p.cfg.Ntfy.Timeout,
	})
	return client.Publish(ctx, topic, m)
}

func (p livePublisher) PublishAttachment(ctx context.Context, topic string, m ntfy.Message, a ntfy.Attachment) (ntfy.Result, error) {
	client := ntfy.New(ntfy.Config{
		BaseURL:   p.cfg.BaseURL(),
		Token:     p.cfg.Ntfy.Token,
		UserAgent: "squawk/" + version.Version,
		Timeout:   p.cfg.Ntfy.Timeout,
	})
	return client.PublishAttachment(ctx, topic, m, a)
}

func (p livePublisher) Health(ctx context.Context) error {
	client := ntfy.New(ntfy.Config{
		BaseURL:   p.cfg.BaseURL(),
		Token:     p.cfg.Ntfy.Token,
		UserAgent: "squawk/" + version.Version,
		Timeout:   p.cfg.Ntfy.Timeout,
	})
	return client.Health(ctx)
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	g, rest, err := extractGlobals(args)
	if err != nil {
		fmt.Fprintf(stderr, "squawk: %v\n", err)
		return 2
	}
	if len(rest) == 0 {
		return runServe(ctx, g, nil, true, false, stdout, stderr)
	}

	switch rest[0] {
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "squawk %s\n", version.Version)
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	case "serve":
		return runServe(ctx, g, rest[1:], false, false, stdout, stderr)
	case "open":
		return runOpen(g, stdout, stderr)
	case "send":
		return runSend(ctx, g, rest[1:], stdout, stderr)
	case "test":
		return runTest(ctx, g, rest[1:], stdout, stderr)
	case "topics":
		return runTopics(ctx, g, rest[1:], stdout, stderr)
	case "config":
		return runConfig(g, rest[1:], stdout, stderr)
	case "service":
		return runService(g, rest[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "squawk: unknown command %q\n\n", rest[0])
		fmt.Fprint(stderr, usage)
		return 2
	}
}

func extractGlobals(args []string) (globals, []string, error) {
	var g globals
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--config":
			if i+1 >= len(args) {
				return g, nil, fmt.Errorf("--config needs a value")
			}
			g.configPath = args[i+1]
			i++
		case strings.HasPrefix(a, "--config="):
			g.configPath = strings.TrimPrefix(a, "--config=")
		case a == "--data-dir":
			if i+1 >= len(args) {
				return g, nil, fmt.Errorf("--data-dir needs a value")
			}
			g.dataDir = args[i+1]
			i++
		case strings.HasPrefix(a, "--data-dir="):
			g.dataDir = strings.TrimPrefix(a, "--data-dir=")
		default:
			rest = append(rest, a)
		}
	}
	return g, rest, nil
}

func load(g globals) (*config.Config, *store.Store, string, string, error) {
	cfgPath, dbPath, err := g.paths()
	if err != nil {
		return nil, nil, "", "", err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, nil, "", "", err
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return nil, nil, "", "", err
	}
	return cfg, st, cfgPath, dbPath, nil
}

func runServe(ctx context.Context, g globals, args []string, openBrowser, allowNonLoopback bool, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", "", "override the bind address")
	allow := fs.Bool("allow-non-loopback", false, "allow binding a non-loopback address")
	open := fs.Bool("open", openBrowser, "open the browser on start")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *allow {
		allowNonLoopback = true
	}
	openBrowser = *open

	cfg, st, cfgPath, _, err := load(g)
	if err != nil {
		fmt.Fprintf(stderr, "squawk: %v\n", err)
		return 1
	}
	if *addr != "" {
		cfg.Server.Addr = *addr
	}
	defer st.Close()

	srv, err := web.New(cfg, st, livePublisher{cfg}, cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "squawk: %v\n", err)
		return 1
	}
	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	if err := srv.Run(runCtx, openBrowser, allowNonLoopback); err != nil {
		fmt.Fprintf(stderr, "squawk: %v\n", err)
		return 1
	}
	return 0
}

func runOpen(g globals, stdout, stderr io.Writer) int {
	cfgPath, _, err := g.paths()
	if err != nil {
		fmt.Fprintf(stderr, "squawk: %v\n", err)
		return 1
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "squawk: %v\n", err)
		return 1
	}
	target := "http://" + cfg.Server.Addr + "/"
	fmt.Fprintln(stdout, target)
	if err := web.OpenURL(target); err != nil {
		fmt.Fprintf(stderr, "squawk: could not open a browser: %v\n", err)
		return 1
	}
	return 0
}

func runSend(ctx context.Context, g globals, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(stderr)
	audienceLabel := fs.String("audience", "", "audience label")
	severityText := fs.String("severity", "", "urgent, warning or info")
	headline := fs.String("headline", "", "alert headline")
	details := fs.String("details", "", "alert details")
	link := fs.String("link", "", "optional http(s) link")
	imagePath := fs.String("image", "", "optional JPEG or PNG to attach")
	dryRun := fs.Bool("dry-run", false, "print without sending")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *audienceLabel == "" || *headline == "" {
		fmt.Fprintln(stderr, "squawk: --audience and --headline are required")
		return 2
	}
	severity, err := format.ParseSeverity(*severityText)
	if err != nil {
		fmt.Fprintf(stderr, "squawk: %v\n", err)
		return 1
	}
	alert := format.Alert{Severity: severity, Headline: *headline, Details: *details, Link: *link}
	if err := alert.Validate(); err != nil {
		fmt.Fprintf(stderr, "squawk: %v\n", err)
		return 1
	}

	cfg, st, _, _, err := load(g)
	if err != nil {
		fmt.Fprintf(stderr, "squawk: %v\n", err)
		return 1
	}
	defer st.Close()

	audience, err := st.GetAudienceByLabel(ctx, *audienceLabel)
	if err != nil {
		fmt.Fprintf(stderr, "squawk: unknown audience %q (try 'squawk topics list')\n", *audienceLabel)
		return 1
	}

	var attachment *ntfy.Attachment
	if *imagePath != "" {
		a, err := imageAttachment(*imagePath)
		if err != nil {
			fmt.Fprintf(stderr, "squawk: %v\n", err)
			return 1
		}
		attachment = a
	} else if severity == format.Seagull {
		attachment = &ntfy.Attachment{Filename: assets.SeagullName, ContentType: assets.SeagullType, Data: assets.Seagull}
	}

	fmt.Fprintln(stdout, alert.Line())
	if *dryRun {
		return 0
	}
	return publish(ctx, cfg, st, audience, alert, attachment, stdout, stderr)
}

func imageAttachment(path string) (*ntfy.Attachment, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, ntfy.MaxAttachmentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > ntfy.MaxAttachmentBytes {
		return nil, fmt.Errorf("image is larger than %d MB", ntfy.MaxAttachmentBytes>>20)
	}
	contentType := http.DetectContentType(data)
	if contentType != "image/jpeg" && contentType != "image/png" {
		return nil, fmt.Errorf("%s is not a JPEG or PNG", path)
	}
	return &ntfy.Attachment{Filename: filepath.Base(path), ContentType: contentType, Data: data}, nil
}

func runTest(ctx context.Context, g globals, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(stderr)
	audienceLabel := fs.String("audience", "", "audience label (default: first)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, st, _, _, err := load(g)
	if err != nil {
		fmt.Fprintf(stderr, "squawk: %v\n", err)
		return 1
	}
	defer st.Close()

	var audience store.Audience
	if *audienceLabel != "" {
		audience, err = st.GetAudienceByLabel(ctx, *audienceLabel)
	} else {
		var all []store.Audience
		all, err = st.ListAudiences(ctx)
		if err == nil && len(all) > 0 {
			audience = all[0]
		} else if err == nil {
			err = store.ErrNotFound
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "squawk: no matching audience\n")
		return 1
	}

	alert := format.Alert{Severity: format.Info, Headline: "Squawk test", Details: "If you can read this, the topic works."}
	fmt.Fprintln(stdout, alert.Line())
	return publish(ctx, cfg, st, audience, alert, nil, stdout, stderr)
}

func publish(ctx context.Context, cfg *config.Config, st *store.Store, audience store.Audience, alert format.Alert, attachment *ntfy.Attachment, stdout, stderr io.Writer) int {
	msg := ntfy.Message{
		Title:    alert.Title(),
		Body:     alert.Body(),
		Priority: alert.Severity.Priority(),
		Tags:     []string{alert.Severity.Tag()},
		Click:    alert.Link,
	}
	var res ntfy.Result
	var sendErr error
	if attachment != nil {
		res, sendErr = livePublisher{cfg}.PublishAttachment(ctx, audience.Topic, msg, *attachment)
	} else {
		res, sendErr = livePublisher{cfg}.Publish(ctx, audience.Topic, msg)
	}

	record := store.Alert{
		Status:     "sent",
		Severity:   string(alert.Severity),
		Audience:   audience.Label,
		Topic:      audience.Topic,
		Title:      msg.Title,
		Body:       msg.Body,
		Line:       alert.Line(),
		Link:       alert.Link,
		Priority:   msg.Priority,
		NtfyID:     res.ID,
		HTTPStatus: res.HTTPStatus,
	}
	if attachment != nil {
		record.AttachmentName = attachment.Filename
		record.AttachmentSize = int64(len(attachment.Data))
	}
	if sendErr != nil {
		record.Status = "failed"
		record.Error = sendErr.Error()
	}
	if _, err := st.RecordAlert(ctx, record); err != nil {
		fmt.Fprintf(stderr, "squawk: could not record the alert: %v\n", err)
	}
	if sendErr != nil {
		fmt.Fprintf(stderr, "squawk: send failed: %v\n", sendErr)
		return 1
	}
	fmt.Fprintf(stdout, "sent to %s (ntfy id %s)\n", audience.Label, res.ID)
	return 0
}

func runTopics(ctx context.Context, g globals, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: squawk topics list|add <label> [topic]|remove <label>")
		return 2
	}
	_, st, _, _, err := load(g)
	if err != nil {
		fmt.Fprintf(stderr, "squawk: %v\n", err)
		return 1
	}
	defer st.Close()

	switch args[0] {
	case "list":
		all, err := st.ListAudiences(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "squawk: %v\n", err)
			return 1
		}
		if len(all) == 0 {
			fmt.Fprintln(stdout, "no audiences")
			return 0
		}
		for _, a := range all {
			fmt.Fprintf(stdout, "%s\t%s\t%s\n", a.Label, a.Topic, a.DefaultSeverity)
		}
		return 0
	case "add":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "usage: squawk topics add <label> [topic]")
			return 2
		}
		label := args[1]
		topic := ""
		if len(args) > 2 {
			topic = args[2]
		}
		if topic == "" {
			topic, err = ntfy.GenerateTopic("squawk-")
			if err != nil {
				fmt.Fprintf(stderr, "squawk: %v\n", err)
				return 1
			}
		}
		if err := ntfy.ValidateTopic(topic); err != nil {
			fmt.Fprintf(stderr, "squawk: %v\n", err)
			return 1
		}
		if _, err := st.CreateAudience(ctx, store.Audience{Label: label, Topic: topic, DefaultSeverity: string(format.Urgent)}); err != nil {
			fmt.Fprintf(stderr, "squawk: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "added %s -> %s\n", label, topic)
		return 0
	case "remove":
		if len(args) < 2 {
			fmt.Fprintln(stderr, "usage: squawk topics remove <label>")
			return 2
		}
		audience, err := st.GetAudienceByLabel(ctx, args[1])
		if err != nil {
			fmt.Fprintf(stderr, "squawk: unknown audience %q\n", args[1])
			return 1
		}
		if err := st.DeleteAudience(ctx, audience.ID); err != nil {
			fmt.Fprintf(stderr, "squawk: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "removed %s\n", args[1])
		return 0
	default:
		fmt.Fprintf(stderr, "squawk: unknown topics command %q\n", args[0])
		return 2
	}
}

func runConfig(g globals, args []string, stdout, stderr io.Writer) int {
	cfgPath, _, err := g.paths()
	if err != nil {
		fmt.Fprintf(stderr, "squawk: %v\n", err)
		return 1
	}
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: squawk config path|show")
		return 2
	}
	switch args[0] {
	case "path":
		fmt.Fprintln(stdout, cfgPath)
		return 0
	case "show":
		cfg, err := config.Load(cfgPath)
		if err != nil {
			fmt.Fprintf(stderr, "squawk: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "base_url: %s\n", cfg.BaseURL())
		fmt.Fprintf(stdout, "token: %s\n", cfg.RedactedToken())
		fmt.Fprintf(stdout, "addr: %s\n", cfg.Server.Addr)
		fmt.Fprintf(stdout, "config: %s\n", cfgPath)
		return 0
	default:
		fmt.Fprintf(stderr, "squawk: unknown config command %q\n", args[0])
		return 2
	}
}

func runService(g globals, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: squawk service install|uninstall|status")
		return 2
	}
	cfgPath, _, err := g.paths()
	if err != nil {
		fmt.Fprintf(stderr, "squawk: %v\n", err)
		return 1
	}
	switch args[0] {
	case "install":
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(stderr, "squawk: %v\n", err)
			return 1
		}
		if err := service.Install(exe, cfgPath); err != nil {
			fmt.Fprintf(stderr, "squawk: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "autostart installed")
		return 0
	case "uninstall":
		if err := service.Uninstall(); err != nil {
			fmt.Fprintf(stderr, "squawk: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "autostart removed")
		return 0
	case "status":
		installed, detail, err := service.Status()
		if err != nil {
			fmt.Fprintf(stderr, "squawk: %v\n", err)
			return 1
		}
		if installed {
			fmt.Fprintf(stdout, "installed (%s)\n", detail)
		} else {
			fmt.Fprintf(stdout, "%s\n", detail)
		}
		return 0
	default:
		fmt.Fprintf(stderr, "squawk: unknown service command %q\n", args[0])
		return 2
	}
}
