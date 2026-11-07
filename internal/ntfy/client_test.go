package ntfy_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/silentfirewall/squawk/internal/ntfy"
)

func TestPublishHeadersAndBody(t *testing.T) {
	var got *http.Request
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"abc123","topic":"squawk-abc"}`)
	}))
	defer srv.Close()

	c := ntfy.New(ntfy.Config{
		BaseURL:     srv.URL + "/",
		Token:       "tk_x",
		UserAgent:   "squawk/test",
		Timeout:     time.Second,
		MaxAttempts: 3,
	})
	res, err := c.Publish(context.Background(), "squawk-abc", ntfy.Message{
		Title:    "🚨 hi",
		Body:     "body text",
		Priority: 5,
		Tags:     []string{"rotating_light"},
		Click:    "https://e.x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Method != "POST" || got.URL.Path != "/squawk-abc" {
		t.Fatalf("%s %s", got.Method, got.URL.Path)
	}
	if got.Header.Get("Title") != "🚨 hi" {
		t.Fatalf("title %q", got.Header.Get("Title"))
	}
	if got.Header.Get("Priority") != "5" || got.Header.Get("Tags") != "rotating_light" {
		t.Fatalf("headers %v", got.Header)
	}
	if got.Header.Get("Click") != "https://e.x" || got.Header.Get("Markdown") != "yes" {
		t.Fatalf("headers %v", got.Header)
	}
	if got.Header.Get("Authorization") != "Bearer tk_x" {
		t.Fatalf("auth %q", got.Header.Get("Authorization"))
	}
	if got.Header.Get("User-Agent") != "squawk/test" {
		t.Fatalf("ua %q", got.Header.Get("User-Agent"))
	}
	if body != "body text" {
		t.Fatalf("body %q", body)
	}
	if res.ID != "abc123" || res.Topic != "squawk-abc" || res.HTTPStatus != 200 {
		t.Fatalf("result %+v", res)
	}
}

func TestPublishOmitsAuthWithoutToken(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		io.WriteString(w, `{"id":"x"}`)
	}))
	defer srv.Close()

	c := ntfy.New(ntfy.Config{BaseURL: srv.URL, Timeout: time.Second, MaxAttempts: 1})
	if _, err := c.Publish(context.Background(), "t", ntfy.Message{Title: "a", Body: "b", Priority: 3}); err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		t.Fatalf("unexpected auth %q", auth)
	}
}

func TestPublishRetries429ThenSucceeds(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		io.WriteString(w, `{"id":"ok"}`)
	}))
	defer srv.Close()

	c := ntfy.New(ntfy.Config{
		BaseURL:     srv.URL,
		Timeout:     time.Second,
		MaxAttempts: 3,
		Backoff:     func(int) time.Duration { return 0 },
	})
	if _, err := c.Publish(context.Background(), "t", ntfy.Message{Title: "x", Body: "y", Priority: 3}); err != nil || n != 2 {
		t.Fatalf("err=%v attempts=%d", err, n)
	}
}

func TestPublishRetries500ThenFails(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"code":50001,"error":"boom"}`)
	}))
	defer srv.Close()

	c := ntfy.New(ntfy.Config{
		BaseURL:     srv.URL,
		Timeout:     time.Second,
		MaxAttempts: 3,
		Backoff:     func(int) time.Duration { return 0 },
	})
	_, err := c.Publish(context.Background(), "t", ntfy.Message{Title: "x", Body: "y", Priority: 3})
	if err == nil || n != 3 {
		t.Fatalf("err=%v attempts=%d", err, n)
	}
}

func TestPublishDoesNotRetry400(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"code":40001,"error":"invalid topic"}`)
	}))
	defer srv.Close()

	c := ntfy.New(ntfy.Config{BaseURL: srv.URL, Timeout: time.Second, MaxAttempts: 3})
	_, err := c.Publish(context.Background(), "t", ntfy.Message{Title: "x", Body: "y", Priority: 3})
	if err == nil || n != 1 {
		t.Fatalf("err=%v attempts=%d", err, n)
	}
	var api *ntfy.APIError
	if !errors.As(err, &api) || api.Message != "invalid topic" || api.Status != 400 {
		t.Fatalf("err %#v", err)
	}
	if strings.Contains(err.Error(), "tk_") {
		t.Fatal("error must not leak a token")
	}
}

func TestValidateTopic(t *testing.T) {
	for _, ok := range []string{"abc", "squawk-ABC_123", strings.Repeat("a", 64)} {
		if err := ntfy.ValidateTopic(ok); err != nil {
			t.Fatalf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", strings.Repeat("a", 65), "bad/topic", "has space", "ümlaut"} {
		if err := ntfy.ValidateTopic(bad); err == nil {
			t.Fatalf("%q must fail", bad)
		}
	}
}

func TestGenerateTopic(t *testing.T) {
	a, err := ntfy.GenerateTopic("squawk-")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ntfy.GenerateTopic("squawk-")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("generated topics must differ")
	}
	if !strings.HasPrefix(a, "squawk-") || len(a) != len("squawk-")+26 {
		t.Fatalf("topic %q", a)
	}
	if err := ntfy.ValidateTopic(a); err != nil {
		t.Fatal(err)
	}
}

func TestHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/health" {
			t.Fatalf("path %q", r.URL.Path)
		}
		io.WriteString(w, `{"healthy":true}`)
	}))
	defer srv.Close()

	c := ntfy.New(ntfy.Config{BaseURL: srv.URL, Timeout: time.Second, MaxAttempts: 1})
	if err := c.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
}
