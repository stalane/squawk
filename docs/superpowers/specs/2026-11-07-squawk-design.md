# squawk — Design

**Date:** 2026-11-07
**Status:** approved 2026-11-07
**Repo:** `/home/david/Documents/squawk` · **GitHub:** `stalane/squawk` · **License:** Apache-2.0

## 1. Intent

A send-only desktop alert publisher. The operator composes one urgent warning,
previews it, and publishes it to a chosen ntfy topic. Subscribers receive it on
their phone or desktop without either side exchanging a phone number or email
address.

The name and icon come from the screaming seagull in `squawk.jpg`.

## 2. Why now

Alerts currently leave the network through ad-hoc `curl` calls to
`https://ntfy.sh` (monero stalls, backup failures). Those are machine alerts.
There is no small, safe tool for a human to send a one-off warning to family or
clients — with a consistent format, the right priority, and a record of what was
sent.

## 3. Users

- **Primary:** the operator (David), sending a warning to a small group.
- **Secondary:** anyone on Linux, macOS or Windows who is handed the binary and a topic name.

## 4. Goals

- **G1** Compose an alert from severity, headline, details and an optional link.
- **G2** Preview exactly what a subscriber will see before sending.
- **G3** Publish to `https://ntfy.sh/<topic>` with the correct title, priority,
  tags and click-through.
- **G4** Keep a local SQLite history of every send, with resend.
- **G5** Manage fixed audiences (label + long random topic) in the UI.
- **G6** Ship as one static binary per OS/arch: no cgo, no runtime dependencies.
- **G7** Bind the UI to loopback only; never send anything but the alert to ntfy.

## 5. Non-goals (v1)

- Reading messages, subscribing, or acting as an ntfy client inbox.
- Accounts, cloud sync, telemetry, analytics.
- Scheduled sends, drafts, attachments, SMS or phone calls.
- A system-tray icon.
- Self-hosted ntfy management (the base URL and optional token are configurable,
  but no server setup is performed).

## 6. Anti-goals

- Silently sending: every send is an explicit action with a preview.
- Sending urgent alerts without a confirmation step.
- Logging or rendering the access token.
- Guessing or truncating a topic name.
- Sending a message larger than ntfy accepts.

## 7. Locked decisions

| Area | Decision |
|---|---|
| Shape | Single Go binary, embedded localhost web UI (`html/template` + HTMX), no cgo |
| Recipe | Follow `/home/david/alertme`: `go:embed`, loopback server, `modernc.org/sqlite`, GoReleaser |
| ntfy server | `https://ntfy.sh` by default; base URL configurable; optional bearer token |
| Format | One line: `<emoji> <headline> -- <details>` (+ ` -- <link>`) |
| On the wire | `Title: <emoji> <headline>`, message body = details (+ link) |
| Severity | urgent = 🚨 / priority 5 / `rotating_light`; warning = ⚠️ / 4 / `warning`; info = ℹ️ / 3 / `information_source` |
| Audiences | Fixed, stored in SQLite, long random topic names |
| History | SQLite, with resend |
| Sharing | Show the topic name only; no QR, no onboarding block |
| Tray | Not in v1 |
| UI theme | xwllz dark/blue, reusing alertme tokens |
| Loopback port | `127.0.0.1:8788` (verified free 2026-11-07) |
| Repo / license | `stalane/squawk`, Apache-2.0 |

## 8. Alert contract

### 8.1 Severity mapping

| Severity | Emoji | ntfy `Priority` | ntfy `Tags` |
|---|---|---|---|
| `urgent` | 🚨 | `5` (max, bypasses Do Not Disturb) | `urgent` |
| `warning` | ⚠️ | `4` (high) | `advisory` |
| `info` | ℹ️ | `3` (default) | `notice` |
| `seagull` | 🐦 | `3` (default) | `seagull` |

Tags deliberately are not ntfy emoji short codes (`rotating_light`, `warning`,
`information_source`, `bird`). ntfy converts a matching tag into an emoji and
prepends it to the title, which would duplicate the emoji already in the title.

### 8.2 Rendered text

- Canonical one-liner (shown in preview and history):
  `<emoji> <headline> -- <details>` and, when a link exists, ` -- <link>`.
- `Title` header: `<emoji> <headline>`.
- Message body: `<details>` and, when a link exists, `\n\n<link>`.

This renders on a phone as the two-part alert the operator sees in the preview,
while the stored canonical string stays the requested one-liner.

### 8.3 Publish request

- `POST {base_url}/{topic}`, body = message body.
- Headers: `Title`, `Priority`, `Tags`, `Markdown: yes`, `Click: <link>` when a
  link exists, `User-Agent: squawk/<version>`, and
  `Authorization: Bearer <token>` when a token is configured.
- Caching is left at ntfy's default so subscribers who are offline at send time
  still receive the alert.
- Message limit: 4096 UTF-8 bytes, enforced before sending with a live counter
  in the UI.
- Retry `429` and `5xx` up to 3 attempts, honouring `Retry-After` and otherwise
  backing off 500 ms, 1 s, 2 s. Never retry `4xx` other than `429`.
- Timeout: 15 s per request.
- Success: parse the JSON response and record `id` (the ntfy message ID).
- Failure: record status and a human-readable error; the alert stays in history
  as `failed` and can be resent.

### 8.4 Topic rules

- Allowed: `[A-Za-z0-9_-]{1,64}`.
- An audience created in the UI gets `squawk-` plus 128 bits of randomness,
  rendered lowercase base32 (26 characters after the prefix).
- The UI warns when a manually entered topic is under 16 characters or matches a
  small blocklist of obvious names (`test`, `alerts`, `squawk`, `urgent`).

## 9. Architecture

```
compose form ──> format.Alert ──> ntfy.Client ──> https://ntfy.sh/<topic>
      │                              │
      └──> SQLite alerts <───────────┘  (record id/status/error)
audiences ──> SQLite audiences
settings  ──> config.yaml (base URL, token, addr)
```

**Components**

- `format` — severity mapping, validation, canonical one-liner, title/body split.
- `ntfy` — topic validation, HTTP publish, retry/backoff, error mapping.
- `store` — SQLite schema and queries for audiences and alerts.
- `config` — YAML load/save, defaults, `0600` permissions, change detection.
- `web` — loopback HTTP server, templates, HTMX partials, CSRF/Host checks.
- `cli` — subcommands, wiring, browser open, service install.
- `service` — platform autostart (Linux systemd user unit, macOS launchd).
- `version` — build-time version string.

## 10. Data model (SQLite)

```sql
CREATE TABLE IF NOT EXISTS audiences (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  label TEXT NOT NULL UNIQUE,
  topic TEXT NOT NULL UNIQUE,
  default_severity TEXT NOT NULL DEFAULT 'urgent',
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS alerts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  created_at TEXT NOT NULL,
  sent_at TEXT,
  status TEXT NOT NULL,              -- sent | failed
  severity TEXT NOT NULL,
  audience TEXT NOT NULL,
  topic TEXT NOT NULL,
  title TEXT NOT NULL,
  body TEXT NOT NULL,
  line TEXT NOT NULL,                -- canonical one-liner
  link TEXT NOT NULL DEFAULT '',
  priority INTEGER NOT NULL,
  ntfy_id TEXT NOT NULL DEFAULT '',
  http_status INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',
  resent_from INTEGER
);
CREATE INDEX IF NOT EXISTS idx_alerts_created_at ON alerts(created_at DESC);
```

The database lives at `<config dir>/squawk/squawk.db`, opened in WAL mode with a
5 s busy timeout.

## 11. Configuration

Path: `<os.UserConfigDir()>/squawk/config.yaml`, mode `0600`. Created with
defaults on first run.

```yaml
ntfy:
  base_url: https://ntfy.sh
  token: ""
  timeout: 15s
server:
  addr: 127.0.0.1:8788
  open_browser: true
```

- Unknown keys are rejected so typos are visible.
- Relative or missing data dir is resolved to the config directory.
- `--data-dir` overrides both the config and database directory (testing and
  portable use); the default is the OS config directory.
- `config show` and `GET /settings` redact the token as `set` / `not set`.

## 12. UI

Shared xwllz tokens from alertme: `--bg #000`, `--card #121a28`,
`--border #1e2a3a`, `--text #e6ecf5`, `--muted #8aa0bd`, `--blue #4f9cf7`,
`--red #ff5a5f`, `--gold #f7b32b`, `--green #2ecc8f`, radius 14px, mono/sans
pairing.

- **Compose `/`** — severity segmented control, audience select, headline,
  details, link; phone-style live preview; byte counter; Ctrl/Cmd+Enter sends;
  urgent reveals an explicit "This bypasses Do Not Disturb" confirmation.
  The seagull photo is the dimmed hero behind the wordmark.
- **History `/history`** — table of sends, filter by severity and free text,
  detail page with the full message, ntfy message ID, status, error and a
  resend button.
- **Audiences `/audiences`** — list, add, edit, delete; generate a random topic;
  per-audience test send; the topic is displayed with a copy button.
- **Settings `/settings`** — base URL, token (write-only), test connection,
  bind address, data directory, config path, autostart state.

Security: loopback bind only, constant-time CSRF check on every POST,
`Host` allowlist (`127.0.0.1`, `localhost`, configured host), no third-party JS,
token never rendered or logged. The server refuses to start on a non-loopback
address unless the operator passes `--allow-non-loopback`.

## 13. CLI

```
squawk                         # serve and open the browser
squawk serve [--addr]          # serve without opening
squawk open                    # open the running UI
squawk send --audience <label> --severity <s> --headline <text> --details <text> [--link <url>] [--dry-run]
squawk test [--audience <label>]   # send an obvious test alert
squawk topics list|add <label> <topic>|remove <label>
squawk config path|show
squawk service install|uninstall|status
squawk version
```

`send` resolves the audience by label, validates everything first, prints the
canonical one-liner, and publishes. `--dry-run` prints without sending.

## 14. Testing

- `format`: severity parsing, emoji/priority/tag mapping, one-liner, title/body,
  byte limit (multi-byte emoji counted correctly), link handling.
- `ntfy`: table tests with `httptest` asserting method, path, headers and body;
  429 with `Retry-After`; 5xx retry; 4xx no-retry; token header; topic
  validation; timeout.
- `store`: audience CRUD and uniqueness; alert record/list/get/search ordering.
- `config`: defaults, round-trip save, `0600`, unknown-key rejection, redaction.
- `web`: CSRF rejection, Host rejection, validation errors, successful send with
  a fake publisher, history and audiences rendering, settings redaction.
- `cli`: `send --dry-run`, unknown audience, invalid severity.
- Cross-compile: `GOOS=linux`, `GOOS=darwin`, `GOOS=windows` builds.
- Manual: one real send to a private test topic, verified on a phone (title,
  emoji, urgent priority).

## 15. Release

- GoReleaser: linux/darwin/windows × amd64/arm64, `CGO_ENABLED=0`, ldflags
  version, tar.gz/zip, `checksums.txt`.
- GitHub Actions on `v*` tags.
- README covers install, first run, subscriber instructions, and the privacy
  caveat that a public ntfy topic name is the only secret.
- Push as `stalane/squawk`; the token already carries `workflow`.
