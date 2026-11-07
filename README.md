# squawk

Send-only urgent alerts over [ntfy](https://ntfy.sh). One static Go binary with a
loopback web UI, local history and resend.

squawk is for the human alert, not the machine alert: compose a warning, pick an
audience, see exactly what subscribers will get, and publish it. Attach a JPEG or
PNG, or let the Seagull severity attach the bird for you. Subscribers use the ntfy
app or web page and never share a phone number or email address.

```
🚨 Crypto is right now being stolen from wallets -- Do not use Safari on iPhone
until you update to iOS 27.2 if you are on a version < 15
```

## Install

```sh
go install github.com/stalane/squawk/cmd/squawk@latest
```

Or download a release binary for Linux, macOS or Windows (amd64/arm64). The
binary is static: no cgo, no runtime dependencies.

## Quick start

```sh
squawk
```

This starts the UI on `http://127.0.0.1:8788` and opens your browser. On a
headless machine use `squawk serve` and open the URL yourself.

1. **Audiences** — add a label. Leave the topic blank and squawk generates a
   long random one. Copy the topic.
2. **Subscribe** — install the ntfy app, add a subscription, and paste the
   topic. Or open `https://ntfy.sh/<topic>` in a browser.
3. **Compose** — choose severity, audience, headline and optional details. Add an
   image if you want one. The preview is what the phone shows. Urgent asks for
   confirmation because it bypasses Do Not Disturb.
4. **History** — every send is stored locally with its ntfy message ID and can be
   resent.

## Severity

| Severity | Emoji | ntfy priority | Use for |
|---|---|---|---|
| Urgent | 🚨 | 5 (max) | active theft, compromise, act now |
| Warning | ⚠️ | 4 (high) | a fix is available, avoid a thing |
| Info | ℹ️ | 3 (default) | context, follow-ups |
| Seagull | 🐦 | 3 (default) | jokes; attaches the seagull image automatically |

## CLI

```sh
squawk send --audience Family --severity urgent \
  --headline "Crypto is being stolen" \
  --details "Do not use Safari on iPhone until you update" \
  --link https://example.com/advisory

squawk send --audience Family --severity info --headline "Test" --details "Hello" --dry-run
squawk send --audience Family --severity seagull --headline "Mine now"
squawk send --audience Family --severity urgent --headline "Act now" --image advisory.jpg
squawk topics list
squawk topics add Clients
squawk test --audience Family
squawk config path
squawk config show
squawk service install
```

Global flags: `--config PATH`, `--data-dir PATH`.

## Privacy and security

- squawk publishes; it never reads messages and has no inbox.
- The web UI binds to loopback only. It refuses a non-loopback address unless
  you pass `--allow-non-loopback`.
- CSRF and Host checks are enforced on a loopback socket; templates escape all
  alert text.
- Images are uploaded to your ntfy server as attachments and expire after three
  hours (ntfy's default); squawk keeps only the filename and size in local history.
- The ntfy topic name **is the only secret**. On the public `ntfy.sh`, anyone who
  knows the topic can read the alerts. Use a generated long random name and
  share it privately. For stronger privacy, self-host ntfy with authentication
  and set the base URL and token in Settings.
- The access token is stored in the config file with `0600` permissions and is
  never displayed or logged.
- Nothing except the alert text, title, priority and optional link leaves your
  machine.

## Development

```sh
make check          # gofmt, vet, tests
make release-check  # cross-compile linux, darwin and windows
make build
```

Design and plan live in `docs/superpowers/`.
