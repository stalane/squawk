# squawk Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship squawk, a send-only ntfy alert publisher as one static Go binary with a loopback web UI, SQLite history and resend.

**Architecture:** Go module `github.com/silentfirewall/squawk` following the alertme recipe: `internal/format` renders the alert, `internal/ntfy` publishes it, `internal/store` records audiences and sends, `internal/web` serves embedded `html/template` + HTMX over loopback, `internal/cli` wires the subcommands. No cgo.

**Tech Stack:** Go 1.26, `gopkg.in/yaml.v3`, `modernc.org/sqlite`, `html/template`, embedded static assets, GoReleaser.

**Spec:** `docs/superpowers/specs/2026-11-07-squawk-design.md`

## Global Constraints

- Module path is `github.com/silentfirewall/squawk`; license Apache-2.0.
- `CGO_ENABLED=0` for every build and release target (linux/darwin/windows × amd64/arm64).
- Default ntfy base URL `https://ntfy.sh`; default UI address `127.0.0.1:8788`.
- Message limit 4096 UTF-8 bytes; headline limit 120 characters; topic `[A-Za-z0-9_-]{1,64}`.
- Severity values are exactly `urgent`, `warning`, `info`.
- Emoji/priority/tags: urgent 🚨/5/`rotating_light`; warning ⚠️/4/`warning`; info ℹ️/3/`information_source`.
- The access token is never logged, rendered, or included in an error string.
- Default config file mode is `0600`.
- No comments in Go code unless the spec requires them.

## Review Focus

1. **XSS in preview/history:** headline, details and link are attacker-controlled text. A payload such as `<img src=x onerror=alert(1)>` must render as text in HTML and never execute.
2. **Token leakage:** `GET /settings`, `config show`, logs and error messages must show only `set`/`not set`, even when ntfy returns a body in a 401.
3. **Base URL with a trailing slash:** `https://ntfy.sh/` must produce `https://ntfy.sh/<topic>`, never `//<topic>`.
4. **UTF-8 byte limit:** an alert whose characters fit but whose bytes exceed 4096 (emoji-heavy) must be rejected before the request, not truncated or sent.
5. **DNS-rebinding Host header:** a request with `Host: evil.example` to the loopback server must be rejected even though the socket is bound to `127.0.0.1`.
6. **Duplicate audience:** adding a label or topic that already exists returns a friendly 400/409 in the UI, not a 500 and not a silent overwrite.

Each line has a test in the task that owns the code.

---

### Task 1: Repository scaffold

**Files:**
- Create: `go.mod`, `.gitignore`, `Makefile`, `.goreleaser.yaml`, `.github/workflows/release.yml`, `README.md`, `SECURITY.md`
- Create: `cmd/squawk/main.go`, `internal/version/version.go`, `internal/version/version_test.go`
- Create: `internal/cli/cli.go` (version and help only)

**Interfaces:**
- Produces: `version.Version` (string, `"dev"` default), `cli.Run(ctx context.Context, args []string, stdout, stderr io.Writer) int`.

- [ ] **Step 1: Initialise the module and dependencies**

```bash
go mod init github.com/silentfirewall/squawk
go get gopkg.in/yaml.v3 modernc.org/sqlite
```

- [ ] **Step 2: Write the version test**

```go
func TestVersionDefault(t *testing.T) {
    if version.Version == "" {
        t.Fatal("version must not be empty")
    }
}
```

- [ ] **Step 3: Implement `internal/version/version.go`**

```go
package version

var Version = "dev"
```

- [ ] **Step 4: Implement `cmd/squawk/main.go`**

Calls `os.Exit(cli.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))`.

- [ ] **Step 5: Implement `cli.Run` handling `version` and `help` only**

Unknown commands print usage and return `2`.

- [ ] **Step 6: Run `go test ./...` and `go build ./...`**

Expected: PASS and a `squawk` binary builds.

- [ ] **Step 7: Add release plumbing**

`.goreleaser.yaml`: `project_name: squawk`, main `./cmd/squawk`, `CGO_ENABLED=0`, six targets, ldflags `-s -w -X github.com/silentfirewall/squawk/internal/version.Version={{.Tag}}`, tar.gz with zip override for windows, checksum. Workflow runs on `v*` tags with `goreleaser/goreleaser-action@v6`. Makefile targets `build`, `test`, `vet`, `fmt`, `release-check`.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "chore: scaffold squawk single-binary Go app"
```

---

### Task 2: Severity and alert formatting

**Files:**
- Create: `internal/format/format.go`, `internal/format/format_test.go`

**Interfaces:**
- Produces:
  - `type Severity string`; constants `Urgent`, `Warning`, `Info`.
  - `func ParseSeverity(s string) (Severity, error)`
  - `func (s Severity) Emoji() string`, `Priority() int`, `Tag() string`, `Label() string`
  - `type Alert struct { Severity Severity; Headline, Details, Link string }`
  - `func (a Alert) Validate() error`
  - `func (a Alert) Title() string` — `<emoji> <headline>`
  - `func (a Alert) Body() string` — details, then `\n\n<link>`
  - `func (a Alert) Line() string` — `<emoji> <headline> -- <details>[ -- <link>]`
  - `func (a Alert) Bytes() int`

- [ ] **Step 1: Write the failing table test**

```go
func TestParseSeverity(t *testing.T) {
    cases := []struct{ in string; want Severity; emoji string; prio int; tag string }{
        {"urgent", Urgent, "🚨", 5, "rotating_light"},
        {"warning", Warning, "⚠️", 4, "warning"},
        {"info", Info, "ℹ️", 3, "information_source"},
    }
    for _, c := range cases {
        got, err := ParseSeverity(c.in)
        if err != nil || got != c.want || got.Emoji() != c.emoji || got.Priority() != c.prio || got.Tag() != c.tag {
            t.Fatalf("%q: got %v/%q/%d/%q err=%v", c.in, got, got.Emoji(), got.Priority(), got.Tag(), err)
        }
    }
    if _, err := ParseSeverity("critical"); err == nil {
        t.Fatal("unknown severity must fail")
    }
}

func TestLineAndSplit(t *testing.T) {
    a := Alert{Severity: Urgent, Headline: "Crypto is being stolen", Details: "Do not use Safari", Link: "https://example.com/x"}
    if a.Title() != "🚨 Crypto is being stolen" { t.Fatalf("title %q", a.Title()) }
    if a.Body() != "Do not use Safari\n\nhttps://example.com/x" { t.Fatalf("body %q", a.Body()) }
    if a.Line() != "🚨 Crypto is being stolen -- Do not use Safari -- https://example.com/x" { t.Fatalf("line %q", a.Line()) }
}

func TestValidateUTF8ByteLimit(t *testing.T) {
    a := Alert{Severity: Urgent, Headline: "x", Details: strings.Repeat("🚨", 1100)}
    if a.Bytes() <= 4096 { t.Fatal("fixture must exceed the limit") }
    if err := a.Validate(); err == nil { t.Fatal("over-limit alert must be rejected") }
}

func TestValidateStripsHeadlineNewlines(t *testing.T) {
    a := Alert{Severity: Info, Headline: "one\ntwo", Details: "body"}
    if strings.Contains(a.Title(), "\n") { t.Fatal("title must be single-line") }
}
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `go test ./internal/format -run 'TestParseSeverity|TestLine|TestValidate' -v`

- [ ] **Step 3: Implement `format.go`**

`Validate` requires a known severity, a non-empty single-line headline of at most 120 characters, non-empty details, and `Bytes() <= 4096`. `Bytes` counts `len(Line())`. Headline newlines are collapsed to spaces in `Title`/`Line`.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/format -v` — PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/format
git commit -m "feat: add alert severity formatting and validation"
```

---

### Task 3: Configuration

**Files:**
- Create: `internal/config/config.go`, `internal/config/config_test.go`

**Interfaces:**
- Produces:
  - `type Config struct { Ntfy Ntfy `yaml:"ntfy"`; Server Server `yaml:"server"` }`
  - `type Ntfy struct { BaseURL string `yaml:"base_url"`; Token string `yaml:"token"`; Timeout time.Duration `yaml:"timeout"` }`
  - `type Server struct { Addr string `yaml:"addr"`; OpenBrowser bool `yaml:"open_browser"` }`
  - `func Default() *Config`
  - `func Path() (string, error)` — `<UserConfigDir>/squawk/config.yaml`
  - `func Load(path string) (*Config, error)` — missing file writes defaults
  - `func (c *Config) Save(path string) error` — `0600`, `yaml.Encoder` with `KnownFields(true)`
  - `func (c *Config) BaseURL() string` — trims trailing `/`
  - `func (c *Config) RedactedToken() string` — `"set"` or `"not set"`

- [ ] **Step 1: Write the tests**

```go
func TestDefaultsAndRoundTrip(t *testing.T) {
    dir := t.TempDir(); p := filepath.Join(dir, "config.yaml")
    c, err := Load(p); if err != nil { t.Fatal(err) }
    if c.Ntfy.BaseURL != "https://ntfy.sh" || c.Server.Addr != "127.0.0.1:8788" { t.Fatalf("bad defaults: %+v", c) }
    c.Ntfy.Token = "tk_secret"
    if err := c.Save(p); err != nil { t.Fatal(err) }
    info, _ := os.Stat(p); if info.Mode().Perm() != 0o600 { t.Fatalf("mode %v", info.Mode().Perm()) }
    got, err := Load(p); if err != nil || got.Ntfy.Token != "tk_secret" { t.Fatalf("roundtrip: %+v %v", got, err) }
}

func TestBaseURLTrimsSlash(t *testing.T) {
    c := Default(); c.Ntfy.BaseURL = "https://ntfy.sh/"
    if c.BaseURL() != "https://ntfy.sh" { t.Fatal(c.BaseURL()) }
}

func TestUnknownKeyRejected(t *testing.T) {
    dir := t.TempDir(); p := filepath.Join(dir, "c.yaml")
    os.WriteFile(p, []byte("ntfy:\n  base_url: x\nbogus: 1\n"), 0o600)
    if _, err := Load(p); err == nil { t.Fatal("unknown key must fail") }
}

func TestRedactedToken(t *testing.T) {
    c := Default(); if c.RedactedToken() != "not set" { t.Fatal("empty") }
    c.Ntfy.Token = "tk_x"; if c.RedactedToken() != "set" { t.Fatal("set") }
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/config -v`

- [ ] **Step 3: Implement `config.go`**

Load applies defaults to zero fields, rejects unknown YAML keys, creates the parent directory, and writes defaults on first run. Save writes through a temp file in the same directory then renames, with mode `0600`.

- [ ] **Step 4: Run tests** — PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config
git commit -m "feat: add YAML configuration with 0600 secrets"
```

---

### Task 4: ntfy publisher

**Files:**
- Create: `internal/ntfy/client.go`, `internal/ntfy/client_test.go`

**Interfaces:**
- Consumes: `format` is not imported here; the caller passes a rendered message.
- Produces:
  - `func ValidateTopic(topic string) error`
  - `func GenerateTopic(prefix string) (string, error)` — `prefix` + lowercase base32 of 16 random bytes
  - `type Message struct { Title, Body string; Priority int; Tags []string; Click string }`
  - `type Result struct { ID, Topic string; HTTPStatus int }`
  - `type Config struct { BaseURL, Token, UserAgent string; Timeout time.Duration; MaxAttempts int }`
  - `func New(cfg Config) *Client`
  - `func (c *Client) Publish(ctx context.Context, topic string, m Message) (Result, error)`
  - `func (c *Client) Health(ctx context.Context) error` — `GET {base}/v1/health`
  - `type APIError struct { Status, Code int; Message string }` implementing `error`

- [ ] **Step 1: Write the tests with `httptest`**

```go
func TestPublishHeadersAndBody(t *testing.T) {
    var got *http.Request; var body string
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        got = r; b, _ := io.ReadAll(r.Body); body = string(b)
        w.Header().Set("Content-Type", "application/json")
        io.WriteString(w, `{"id":"abc123","topic":"t"}`)
    }))
    defer srv.Close()
    c := New(Config{BaseURL: srv.URL + "/", Token: "tk_x", UserAgent: "squawk/test", Timeout: time.Second, MaxAttempts: 3})
    res, err := c.Publish(context.Background(), "squawk-abc", Message{Title: "🚨 hi", Body: "body", Priority: 5, Tags: []string{"rotating_light"}, Click: "https://e.x"})
    if err != nil { t.Fatal(err) }
    if got.Method != "POST" || got.URL.Path != "/squawk-abc" { t.Fatalf("%s %s", got.Method, got.URL.Path) }
    if got.Header.Get("Title") != "🚨 hi" || got.Header.Get("Priority") != "5" || got.Header.Get("Tags") != "rotating_light" || got.Header.Get("Click") != "https://e.x" { t.Fatalf("headers %v", got.Header) }
    if got.Header.Get("Authorization") != "Bearer tk_x" { t.Fatal("missing auth") }
    if body != "body" { t.Fatalf("body %q", body) }
    if res.ID != "abc123" { t.Fatalf("id %q", res.ID) }
}

func TestPublishRetries429ThenSucceeds(t *testing.T) {
    n := 0
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        n++
        if n == 1 { w.Header().Set("Retry-After", "0"); w.WriteHeader(429); return }
        io.WriteString(w, `{"id":"ok"}`)
    }))
    defer srv.Close()
    c := New(Config{BaseURL: srv.URL, Timeout: time.Second, MaxAttempts: 3, Backoff: func(int) time.Duration { return 0 }})
    if _, err := c.Publish(context.Background(), "t", Message{Title: "x", Body: "y", Priority: 3}); err != nil || n != 2 { t.Fatalf("err=%v attempts=%d", err, n) }
}

func TestPublishDoesNotRetry400(t *testing.T) {
    n := 0
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        n++; w.WriteHeader(400); io.WriteString(w, `{"code":40001,"error":"invalid topic"}`)
    }))
    defer srv.Close()
    c := New(Config{BaseURL: srv.URL, Timeout: time.Second, MaxAttempts: 3})
    _, err := c.Publish(context.Background(), "t", Message{Title: "x", Body: "y", Priority: 3})
    if err == nil || n != 1 { t.Fatalf("err=%v attempts=%d", err, n) }
    var api *APIError; if !errors.As(err, &api) || api.Message != "invalid topic" { t.Fatalf("err %v", err) }
}

func TestValidateTopic(t *testing.T) {
    for _, ok := range []string{"abc", "squawk-ABC_123", strings.Repeat("a", 64)} {
        if err := ValidateTopic(ok); err != nil { t.Fatalf("%q: %v", ok, err) }
    }
    for _, bad := range []string{"", strings.Repeat("a", 65), "bad/topic", "has space", "ümlaut"} {
        if err := ValidateTopic(bad); err == nil { t.Fatalf("%q must fail", bad) }
    }
}
```

Add `Backoff func(attempt int) time.Duration` to `Config` so tests do not sleep; production default is `500ms * 2^(attempt-1)` with `Retry-After` taking precedence.

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/ntfy -v`

- [ ] **Step 3: Implement `client.go`**

`Publish` validates the topic, trims a trailing `/` from the base URL, sets all headers, and sends the body. Retry only on `429` or `>=500`. Parse `{code,error}` and `{id}` JSON. Cap the response body read at 64 KiB. Never include the token or request headers in the error.

- [ ] **Step 4: Run tests** — PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/ntfy
git commit -m "feat: add ntfy publisher with retry and topic validation"
```

---

### Task 5: SQLite store

**Files:**
- Create: `internal/store/store.go`, `internal/store/store_test.go`

**Interfaces:**
- Produces:
  - `type Audience struct { ID int64; Label, Topic, DefaultSeverity string; CreatedAt time.Time }`
  - `type Alert struct { ID int64; CreatedAt, SentAt time.Time; Status, Severity, Audience, Topic, Title, Body, Line, Link string; Priority int; NtfyID string; HTTPStatus int; Error string; ResentFrom int64 }`
  - `func Open(path string) (*Store, error)` and `func (s *Store) Close() error`
  - `CreateAudience(ctx, Audience) (int64, error)`, `ListAudiences(ctx) ([]Audience, error)`, `GetAudience(ctx, id int64) (Audience, error)`, `GetAudienceByLabel(ctx, label string) (Audience, error)`, `UpdateAudience(ctx, Audience) error`, `DeleteAudience(ctx, id int64) error`
  - `RecordAlert(ctx, Alert) (int64, error)`, `ListAlerts(ctx, limit int) ([]Alert, error)`, `GetAlert(ctx, id int64) (Alert, error)`, `SearchAlerts(ctx, query, severity string, limit int) ([]Alert, error)`

- [ ] **Step 1: Write the tests**

```go
func TestAudienceCRUDAndUniqueness(t *testing.T) {
    s := newTestStore(t)
    id, err := s.CreateAudience(ctx, Audience{Label: "Family", Topic: "squawk-family-abc", DefaultSeverity: "urgent"})
    if err != nil || id == 0 { t.Fatalf("%d %v", id, err) }
    if _, err := s.CreateAudience(ctx, Audience{Label: "Family", Topic: "other"}); err == nil {
        t.Fatal("duplicate label must fail")
    }
    if _, err := s.CreateAudience(ctx, Audience{Label: "Clients", Topic: "squawk-family-abc"}); err == nil {
        t.Fatal("duplicate topic must fail")
    }
    got, err := s.GetAudienceByLabel(ctx, "Family")
    if err != nil || got.Topic != "squawk-family-abc" { t.Fatalf("%+v %v", got, err) }
    got.Label = "Close family"
    if err := s.UpdateAudience(ctx, got); err != nil { t.Fatal(err) }
    if err := s.DeleteAudience(ctx, id); err != nil { t.Fatal(err) }
    if _, err := s.GetAudience(ctx, id); err == nil { t.Fatal("deleted audience must not be found") }
}

func TestRecordAndSearchAlerts(t *testing.T) {
    s := newTestStore(t)
    for i, sev := range []string{"urgent", "info", "urgent"} {
        if _, err := s.RecordAlert(ctx, Alert{Status: "sent", Severity: sev, Audience: "Family", Topic: "t", Title: "T", Body: "B", Line: "L", Priority: 5, NtfyID: fmt.Sprintf("id%d", i)}); err != nil { t.Fatal(err) }
    }
    all, err := s.ListAlerts(ctx, 10); if err != nil || len(all) != 3 { t.Fatalf("%d %v", len(all), err) }
    if all[0].NtfyID != "id2" { t.Fatalf("newest first, got %q", all[0].NtfyID) }
    urg, err := s.SearchAlerts(ctx, "", "urgent", 10); if err != nil || len(urg) != 2 { t.Fatalf("%d %v", len(urg), err) }
    txt, err := s.SearchAlerts(ctx, "B", "", 10); if err != nil || len(txt) != 3 { t.Fatalf("%d %v", len(txt), err) }
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/store -v`

- [ ] **Step 3: Implement `store.go`**

Apply the schema from the spec with `CREATE TABLE IF NOT EXISTS`. Set `PRAGMA journal_mode=WAL` and `busy_timeout=5000`. Store timestamps as RFC3339 UTC. Map `UNIQUE` constraint failures to a sentinel `ErrConflict` so the web layer can return 409 without parsing driver text.

- [ ] **Step 4: Run tests** — PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "feat: add SQLite store for audiences and alert history"
```

---

### Task 6: Web UI

**Files:**
- Create: `internal/web/embed.go`, `internal/web/server.go`, `internal/web/server_test.go`
- Create: `internal/web/templates/{layout,compose,history,detail,audiences,settings}.html`
- Create: `internal/web/static/{style.css,app.js}` and copy `assets/icon.png`

**Interfaces:**
- Consumes: `config.Config`, `store.Store`, `format.Alert`, `ntfy.Client`.
- Produces:
  - `type Publisher interface { Publish(ctx context.Context, topic string, m ntfy.Message) (ntfy.Result, error); Health(ctx context.Context) error }`
  - `type Server struct { ... }`
  - `func New(cfg *config.Config, st *store.Store, pub Publisher, cfgPath string) (*Server, error)`
  - `func (s *Server) Handler() http.Handler`
  - `func (s *Server) Run(ctx context.Context, openBrowser bool) error`

- [ ] **Step 1: Write handler tests**

```go
func TestCSRFRejected(t *testing.T) {
    s := newTestServer(t)
    req := httptest.NewRequest("POST", "/send", strings.NewReader("headline=x&details=y"))
    req.Host = "127.0.0.1:8788"
    rec := httptest.NewRecorder()
    s.Handler().ServeHTTP(rec, req)
    if rec.Code != http.StatusForbidden { t.Fatalf("got %d", rec.Code) }
}

func TestHostRejected(t *testing.T) {
    s := newTestServer(t)
    req := httptest.NewRequest("GET", "/", nil); req.Host = "evil.example"
    rec := httptest.NewRecorder(); s.Handler().ServeHTTP(rec, req)
    if rec.Code != http.StatusForbidden { t.Fatalf("got %d", rec.Code) }
}

func TestSendSuccess(t *testing.T) {
    pub := &fakePublisher{}
    s := newTestServerWith(t, pub)
    body := url.Values{"csrf": {s.CSRF()}, "severity": {"urgent"}, "audience": {"Family"}, "headline": {"Crypto stolen"}, "details": {"Do not use Safari"}, "link": {""}}
    req := httptest.NewRequest("POST", "/send", strings.NewReader(body.Encode()))
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded"); req.Host = "127.0.0.1:8788"
    rec := httptest.NewRecorder(); s.Handler().ServeHTTP(rec, req)
    if rec.Code != http.StatusSeeOther { t.Fatalf("got %d: %s", rec.Code, rec.Body.String()) }
    if pub.last.Title != "🚨 Crypto stolen" || pub.last.Priority != 5 { t.Fatalf("%+v", pub.last) }
}

func TestXSSEscaped(t *testing.T) {
    s := newTestServerWith(t, &fakePublisher{})
    body := url.Values{"csrf": {s.CSRF()}, "severity": {"info"}, "audience": {"Family"}, "headline": {`<img src=x onerror=alert(1)>`}, "details": {"d"}}
    req := httptest.NewRequest("POST", "/send", strings.NewReader(body.Encode()))
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded"); req.Host = "127.0.0.1:8788"
    rec := httptest.NewRecorder(); s.Handler().ServeHTTP(rec, req)
    if strings.Contains(rec.Body.String(), "<img src=x") { t.Fatal("unescaped payload in response") }
}

func TestDuplicateAudienceIsConflict(t *testing.T) {
    s := newTestServerWith(t, &fakePublisher{})
    body := url.Values{"csrf": {s.CSRF()}, "label": {"Family"}, "topic": {"squawk-family-abc"}, "default_severity": {"urgent"}}
    for i := 0; i < 2; i++ {
        req := httptest.NewRequest("POST", "/audiences", strings.NewReader(body.Encode()))
        req.Header.Set("Content-Type", "application/x-www-form-urlencoded"); req.Host = "127.0.0.1:8788"
        rec := httptest.NewRecorder(); s.Handler().ServeHTTP(rec, req)
        if i == 1 && rec.Code != http.StatusConflict && rec.Code != http.StatusBadRequest {
            t.Fatalf("duplicate got %d", rec.Code)
        }
    }
}

func TestSettingsRedactsToken(t *testing.T) {
    s := newTestServerWithToken(t, "tk_secret")
    req := httptest.NewRequest("GET", "/settings", nil); req.Host = "127.0.0.1:8788"
    rec := httptest.NewRecorder(); s.Handler().ServeHTTP(rec, req)
    if strings.Contains(rec.Body.String(), "tk_secret") { t.Fatal("token leaked") }
}
```

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/web -v`

- [ ] **Step 3: Implement the server and templates**

Routes: `GET /`, `POST /send`, `GET /history`, `GET /alerts/{id}`, `POST /alerts/{id}/resend`, `GET /audiences`, `POST /audiences`, `POST /audiences/{id}/delete`, `POST /audiences/{id}/test`, `GET /settings`, `POST /settings`, `GET /healthz`. Every POST checks `csrf` in constant time and redirects with `303`. A `Host` middleware allows only `127.0.0.1[:port]`, `localhost[:port]`, and the configured bind host. Templates escape by default; the preview is rendered by the same template path as history so both are covered by the XSS test. Settings renders `RedactedToken()`. Compose with no audiences shows a link to create one instead of a send button.

- [ ] **Step 4: Implement the xwllz theme and compose behaviour**

`style.css` uses the spec's tokens. `app.js` updates the preview from the form fields, the byte counter, and reveals the urgent confirmation. The seagull photo is the hero background with a dark gradient.

- [ ] **Step 5: Run tests** — PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/web assets
git commit -m "feat: add loopback web UI with compose, history and audiences"
```

---

### Task 7: CLI and wiring

**Files:**
- Modify: `internal/cli/cli.go`
- Create: `internal/cli/cli_test.go`, `internal/cli/browser.go`

**Interfaces:**
- Consumes: everything above.
- Produces: `cli.Run` handles `serve`, `open`, `send`, `test`, `topics`, `config`, `service`, `version`, `help`.

- [ ] **Step 1: Write tests**

```go
func TestSendDryRun(t *testing.T) {
    dir := t.TempDir()
    var out bytes.Buffer
    code := Run(context.Background(), []string{"send", "--data-dir", dir, "--audience", "Family", "--severity", "urgent", "--headline", "H", "--details", "D", "--dry-run"}, &out, &out)
    if code != 0 || !strings.Contains(out.String(), "🚨 H -- D") { t.Fatalf("code=%d out=%q", code, out.String()) }
}

func TestSendUnknownAudience(t *testing.T) {
    var out bytes.Buffer
    code := Run(context.Background(), []string{"send", "--data-dir", t.TempDir(), "--audience", "Nope", "--severity", "info", "--headline", "H", "--details", "D", "--dry-run"}, &out, &out)
    if code == 0 { t.Fatal("unknown audience must fail") }
}

func TestInvalidSeverity(t *testing.T) {
    var out bytes.Buffer
    code := Run(context.Background(), []string{"send", "--data-dir", t.TempDir(), "--audience", "Family", "--severity", "critical", "--headline", "H", "--details", "D", "--dry-run"}, &out, &out)
    if code == 0 { t.Fatal("invalid severity must fail") }
}
```

The tests seed an audience in the temp dir's database first via `store.Open` and `CreateAudience`.

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/cli -v`

- [ ] **Step 3: Implement the subcommands and browser opener**

`--data-dir` and `--config` are persistent flags. `serve` loads config and store, constructs the ntfy client, and runs the server. `open` prints the URL and opens the browser. `send` resolves the audience, builds `format.Alert`, validates, prints `Line()`, and publishes unless `--dry-run`. On failure it prints the error and returns `1`.

- [ ] **Step 4: Run tests** — PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli
git commit -m "feat: add CLI subcommands and browser launch"
```

---

### Task 8: Autostart service

**Files:**
- Create: `internal/service/service.go`, `internal/service/service_test.go`, `internal/service/linux.go`, `internal/service/darwin.go`, `internal/service/windows.go`

**Interfaces:**
- Produces: `func Install(exePath, configPath string) error`, `func Uninstall() error`, `func Status() (installed bool, detail string, err error)`.

- [ ] **Step 1: Write a test that renders the Linux unit**

```go
func TestLinuxUnit(t *testing.T) {
    unit := linuxUnit("/home/u/.local/bin/squawk", "/home/u/.config/squawk/config.yaml")
    if !strings.Contains(unit, "ExecStart=/home/u/.local/bin/squawk serve --config /home/u/.config/squawk/config.yaml") {
        t.Fatalf("unit: %s", unit)
    }
    if !strings.Contains(unit, "Restart=on-failure") { t.Fatal("missing restart policy") }
}
```

- [ ] **Step 2: Implement the unit/plist renderers and install paths**

Linux: `~/.config/systemd/user/squawk.service`, then `systemctl --user daemon-reload` and `enable --now`. macOS: `~/Library/LaunchAgents/com.silentfirewall.squawk.plist`, then `launchctl load`. Windows returns "autostart is not supported on Windows yet".

- [ ] **Step 3: Run tests** — PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/service
git commit -m "feat: add Linux and macOS autostart"
```

---

### Task 9: Assets, README, release verification

**Files:**
- Create: `assets/icon.png`, `internal/web/static/icon.png`, `assets/icon-source.md`
- Modify: `README.md`, `SECURITY.md`

- [ ] **Step 1: Derive the icon**

Crop `assets/squawk.jpg` to a square around the head (centre ≈ 700,420; side ≈ 700), resize to 512×512 PNG, and write both copies. Record the crop command in `assets/icon-source.md`.

- [ ] **Step 2: Write the README**

Install, first run, create an audience, subscribe on a phone, send, history/resend, CLI examples, privacy caveat (public topic name = the only secret), self-hosted base URL note.

- [ ] **Step 3: Run the full verification**

```bash
gofmt -l . && go vet ./... && go test ./... && \
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build ./cmd/squawk && \
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build ./cmd/squawk && \
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./cmd/squawk
```

Expected: no `gofmt` output, vet clean, all tests PASS, all three builds succeed.

- [ ] **Step 4: Live smoke test**

Run the server, create one audience with a random test topic, send one `info` test alert, confirm it in the ntfy web view, then send one `urgent` alert and confirm the phone shows priority max. Delete the test audience.

- [ ] **Step 5: Commit and tag**

```bash
git add -A
git commit -m "docs: add README, icon and release verification"
git tag v0.1.0
```

- [ ] **Step 6: Publish**

`gh auth switch --user silentfirewall`, create `silentfirewall/squawk` (private until the operator says public), push `main` and the tag, and confirm the release workflow produces six archives plus checksums.
