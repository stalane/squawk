package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silentfirewall/squawk/internal/config"
	"github.com/silentfirewall/squawk/internal/ntfy"
	"github.com/silentfirewall/squawk/internal/store"
	"github.com/silentfirewall/squawk/internal/web"
)

var ctx = context.Background()

type fakePublisher struct {
	last      ntfy.Message
	lastTopic string
	calls     int
	err       error
}

func (f *fakePublisher) Publish(_ context.Context, topic string, m ntfy.Message) (ntfy.Result, error) {
	f.calls++
	f.last = m
	f.lastTopic = topic
	if f.err != nil {
		return ntfy.Result{HTTPStatus: 500}, f.err
	}
	return ntfy.Result{ID: "ntfy-id", Topic: topic, HTTPStatus: 200}, nil
}

func (f *fakePublisher) Health(context.Context) error { return nil }

type harness struct {
	server *web.Server
	store  *store.Store
	cfg    *config.Config
	pub    *fakePublisher
}

func newHarness(t *testing.T, seed bool, token string) *harness {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := config.Default()
	cfg.Ntfy.Token = token
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "squawk.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if seed {
		if _, err := st.CreateAudience(ctx, store.Audience{Label: "Family", Topic: "squawk-family-abc", DefaultSeverity: "urgent"}); err != nil {
			t.Fatal(err)
		}
	}
	pub := &fakePublisher{}
	srv, err := web.New(cfg, st, pub, cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	return &harness{server: srv, store: st, cfg: cfg, pub: pub}
}

func (h *harness) do(req *http.Request) *httptest.ResponseRecorder {
	req.Host = "127.0.0.1:8788"
	rec := httptest.NewRecorder()
	h.server.Handler().ServeHTTP(rec, req)
	return rec
}

func form(values url.Values) *http.Request {
	req := httptest.NewRequest("POST", "/", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func TestCSRFRejected(t *testing.T) {
	h := newHarness(t, true, "")
	req := form(url.Values{"headline": {"x"}, "details": {"y"}})
	req.URL.Path = "/send"
	rec := h.do(req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("got %d", rec.Code)
	}
	if h.pub.calls != 0 {
		t.Fatal("must not publish without CSRF")
	}
}

func TestHostRejected(t *testing.T) {
	h := newHarness(t, true, "")
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "evil.example"
	rec := httptest.NewRecorder()
	h.server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestSendSuccess(t *testing.T) {
	h := newHarness(t, true, "")
	req := form(url.Values{
		"csrf":     {h.server.CSRF()},
		"severity": {"urgent"},
		"audience": {"Family"},
		"headline": {"Crypto stolen"},
		"details":  {"Do not use Safari"},
		"confirm":  {"on"},
	})
	req.URL.Path = "/send"
	rec := h.do(req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	if h.pub.last.Title != "🚨 Crypto stolen" || h.pub.last.Priority != 5 {
		t.Fatalf("message %+v", h.pub.last)
	}
	if h.pub.lastTopic != "squawk-family-abc" {
		t.Fatalf("topic %q", h.pub.lastTopic)
	}
	if !strings.HasPrefix(rec.Header().Get("Location"), "/alerts/") {
		t.Fatalf("location %q", rec.Header().Get("Location"))
	}
}

func TestUrgentRequiresConfirmation(t *testing.T) {
	h := newHarness(t, true, "")
	req := form(url.Values{
		"csrf":     {h.server.CSRF()},
		"severity": {"urgent"},
		"audience": {"Family"},
		"headline": {"Crypto stolen"},
		"details":  {"Do not use Safari"},
	})
	req.URL.Path = "/send"
	rec := h.do(req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d", rec.Code)
	}
	if h.pub.calls != 0 {
		t.Fatal("must not publish unconfirmed urgent alert")
	}
}

func TestXSSEscaped(t *testing.T) {
	h := newHarness(t, true, "")
	req := form(url.Values{
		"csrf":     {h.server.CSRF()},
		"severity": {"info"},
		"audience": {"Family"},
		"headline": {`<img src=x onerror=alert(1)>`},
		"details":  {"d"},
	})
	req.URL.Path = "/send"
	rec := h.do(req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("send: %d", rec.Code)
	}

	detail := httptest.NewRequest("GET", rec.Header().Get("Location"), nil)
	page := h.do(detail)
	if strings.Contains(page.Body.String(), "<img src=x") {
		t.Fatal("unescaped payload in detail page")
	}
	if !strings.Contains(page.Body.String(), "&lt;img") {
		t.Fatal("expected the payload to be escaped, not dropped")
	}
}

func TestDuplicateAudienceIsConflict(t *testing.T) {
	h := newHarness(t, false, "")
	values := url.Values{
		"csrf":             {h.server.CSRF()},
		"label":            {"Clients"},
		"topic":            {"squawk-clients-abc"},
		"default_severity": {"urgent"},
	}
	for i := 0; i < 2; i++ {
		req := form(values)
		req.URL.Path = "/audiences"
		rec := h.do(req)
		if i == 0 && rec.Code != http.StatusSeeOther {
			t.Fatalf("first create: %d", rec.Code)
		}
		if i == 1 && rec.Code != http.StatusConflict {
			t.Fatalf("duplicate: %d", rec.Code)
		}
	}
}

func TestSettingsRedactsToken(t *testing.T) {
	h := newHarness(t, true, "tk_secret")
	req := httptest.NewRequest("GET", "/settings", nil)
	rec := h.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "tk_secret") {
		t.Fatal("token leaked into settings page")
	}
	if !strings.Contains(rec.Body.String(), "set") {
		t.Fatal("token status missing")
	}
}

func TestComposeWithNoAudiences(t *testing.T) {
	h := newHarness(t, false, "")
	req := httptest.NewRequest("GET", "/", nil)
	rec := h.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Create one first") {
		t.Fatal("expected guidance to create an audience")
	}
}
