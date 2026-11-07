package ntfy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	maxResponseBytes   = 64 << 10
	MaxAttachmentBytes = 15 << 20
)

var topicPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type Message struct {
	Title    string
	Body     string
	Priority int
	Tags     []string
	Click    string
}

type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

type Result struct {
	ID         string
	Topic      string
	HTTPStatus int
}

type Config struct {
	BaseURL     string
	Token       string
	UserAgent   string
	Timeout     time.Duration
	MaxAttempts int
	Backoff     func(attempt int) time.Duration
}

type APIError struct {
	Status  int
	Code    int
	Message string
}

func (e *APIError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("ntfy: HTTP %d (code %d): %s", e.Status, e.Code, e.Message)
	}
	return fmt.Sprintf("ntfy: HTTP %d: %s", e.Status, e.Message)
}

type Client struct {
	cfg Config
	hc  *http.Client
}

func New(cfg Config) *Client {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.Backoff == nil {
		cfg.Backoff = func(attempt int) time.Duration {
			return 500 * time.Millisecond * time.Duration(1<<(attempt-1))
		}
	}
	return &Client{cfg: cfg, hc: &http.Client{Timeout: cfg.Timeout}}
}

func (c *Client) Publish(ctx context.Context, topic string, m Message) (Result, error) {
	return c.publish(ctx, topic, m, nil)
}

func (c *Client) PublishAttachment(ctx context.Context, topic string, m Message, a Attachment) (Result, error) {
	if a.Filename == "" {
		return Result{}, errors.New("attachment filename is required")
	}
	if len(a.Data) == 0 {
		return Result{}, errors.New("attachment is empty")
	}
	if len(a.Data) > MaxAttachmentBytes {
		return Result{}, fmt.Errorf("attachment is %d bytes; the limit is %d", len(a.Data), MaxAttachmentBytes)
	}
	return c.publish(ctx, topic, m, &a)
}

func (c *Client) publish(ctx context.Context, topic string, m Message, a *Attachment) (Result, error) {
	if err := ValidateTopic(topic); err != nil {
		return Result{}, err
	}
	if err := ValidateBaseURL(c.cfg.BaseURL); err != nil {
		return Result{}, err
	}
	base := strings.TrimRight(c.cfg.BaseURL, "/")
	if base == "" {
		return Result{}, errors.New("ntfy base URL is not configured")
	}
	endpoint := base + "/" + topic

	method := http.MethodPost
	reader := func() io.Reader { return strings.NewReader(m.Body) }
	if a != nil {
		method = http.MethodPut
		reader = func() io.Reader { return bytes.NewReader(a.Data) }
	}

	var lastErr error
	for attempt := 1; attempt <= c.cfg.MaxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, endpoint, reader())
		if err != nil {
			return Result{}, err
		}
		if a != nil {
			ct := a.ContentType
			if ct == "" {
				ct = "application/octet-stream"
			}
			req.Header.Set("Content-Type", ct)
			req.Header.Set("Filename", a.Filename)
			req.Header.Set("Message", m.Body)
		} else {
			req.Header.Set("Content-Type", "text/plain; charset=utf-8")
		}
		req.Header.Set("Title", m.Title)
		req.Header.Set("Priority", strconv.Itoa(m.Priority))
		req.Header.Set("Markdown", "yes")
		if len(m.Tags) > 0 {
			req.Header.Set("Tags", strings.Join(m.Tags, ","))
		}
		if m.Click != "" {
			req.Header.Set("Click", m.Click)
		}
		if c.cfg.UserAgent != "" {
			req.Header.Set("User-Agent", c.cfg.UserAgent)
		}
		if c.cfg.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
		}

		resp, err := c.hc.Do(req)
		if err != nil {
			lastErr = err
			if attempt == c.cfg.MaxAttempts {
				break
			}
			if err := sleepCtx(ctx, c.cfg.Backoff(attempt)); err != nil {
				return Result{}, err
			}
			continue
		}

		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			if attempt == c.cfg.MaxAttempts {
				break
			}
			if err := sleepCtx(ctx, c.cfg.Backoff(attempt)); err != nil {
				return Result{}, err
			}
			continue
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			var out struct {
				ID    string `json:"id"`
				Topic string `json:"topic"`
			}
			if len(bytes.TrimSpace(data)) > 0 {
				_ = json.Unmarshal(data, &out)
			}
			if out.Topic == "" {
				out.Topic = topic
			}
			return Result{ID: out.ID, Topic: out.Topic, HTTPStatus: resp.StatusCode}, nil
		}

		apiErr := decodeAPIError(resp.StatusCode, data)
		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			return Result{HTTPStatus: resp.StatusCode}, apiErr
		}

		lastErr = apiErr
		if attempt == c.cfg.MaxAttempts {
			break
		}
		delay := c.cfg.Backoff(attempt)
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, err := strconv.Atoi(strings.TrimSpace(ra)); err == nil && secs >= 0 {
				delay = time.Duration(secs) * time.Second
			}
		}
		if err := sleepCtx(ctx, delay); err != nil {
			return Result{HTTPStatus: resp.StatusCode}, err
		}
	}

	return Result{}, lastErr
}

func (c *Client) Health(ctx context.Context) error {
	if err := ValidateBaseURL(c.cfg.BaseURL); err != nil {
		return err
	}
	base := strings.TrimRight(c.cfg.BaseURL, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/health", nil)
	if err != nil {
		return err
	}
	if c.cfg.UserAgent != "" {
		req.Header.Set("User-Agent", c.cfg.UserAgent)
	}
	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeAPIError(resp.StatusCode, data)
	}
	return nil
}

func ValidateBaseURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("ntfy base URL is not configured")
	}
	u, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil || u.Host == "" {
		return errors.New("invalid ntfy base URL")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if loopbackHost(u.Hostname()) {
			return nil
		}
		return errors.New("ntfy base URL must use https (plain http is allowed only for localhost)")
	default:
		return errors.New("ntfy base URL must use https")
	}
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func ValidateTopic(topic string) error {
	if !topicPattern.MatchString(topic) {
		return fmt.Errorf("invalid topic %q: use 1-64 letters, digits, '-' or '_'", topic)
	}
	return nil
}

func GenerateTopic(prefix string) (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	topic := prefix + strings.ToLower(enc)
	if err := ValidateTopic(topic); err != nil {
		return "", err
	}
	return topic, nil
}

func decodeAPIError(status int, data []byte) *APIError {
	var payload struct {
		Code  int    `json:"code"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &payload); err == nil && payload.Error != "" {
		return &APIError{Status: status, Code: payload.Code, Message: payload.Error}
	}
	msg := strings.TrimSpace(string(data))
	if msg == "" {
		msg = http.StatusText(status)
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return &APIError{Status: status, Message: msg}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
