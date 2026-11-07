package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/silentfirewall/squawk/internal/config"
	"github.com/silentfirewall/squawk/internal/format"
	"github.com/silentfirewall/squawk/internal/ntfy"
	"github.com/silentfirewall/squawk/internal/store"
	"github.com/silentfirewall/squawk/internal/version"
)

type Publisher interface {
	Publish(ctx context.Context, topic string, m ntfy.Message) (ntfy.Result, error)
	Health(ctx context.Context) error
}

type Server struct {
	cfg     *config.Config
	store   *store.Store
	pub     Publisher
	cfgPath string
	dataDir string
	csrf    string
	pages   map[string]*template.Template
	static  http.Handler
}

type composeForm struct {
	Severity string
	Audience string
	Headline string
	Details  string
	Link     string
}

type composeData struct {
	Page            string
	CSRF            string
	Audiences       []store.Audience
	Form            composeForm
	Error           string
	Notice          string
	DefaultSeverity string
	HasAudiences    bool
}

type historyData struct {
	Page     string
	Alerts   []store.Alert
	Query    string
	Severity string
}

type detailData struct {
	Page  string
	CSRF  string
	Alert store.Alert
}

type audiencesData struct {
	Page      string
	CSRF      string
	Audiences []store.Audience
	Error     string
	Notice    string
	Generated string
}

type settingsData struct {
	Page        string
	CSRF        string
	BaseURL     string
	TokenStatus string
	Addr        string
	OpenBrowser bool
	DataDir     string
	ConfigPath  string
	Version     string
	Error       string
	Notice      string
}

func New(cfg *config.Config, st *store.Store, pub Publisher, cfgPath string) (*Server, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(raw)

	funcs := template.FuncMap{
		"datetime": func(t time.Time) string {
			if t.IsZero() {
				return "—"
			}
			return t.Local().Format("2006-01-02 15:04")
		},
		"lower": strings.ToLower,
	}

	pages := map[string]*template.Template{}
	for _, name := range []string{"compose", "history", "detail", "audiences", "settings"} {
		t, err := template.New("layout").Funcs(funcs).ParseFS(assets, "templates/layout.html", "templates/"+name+".html")
		if err != nil {
			return nil, fmt.Errorf("parse %s template: %w", name, err)
		}
		pages[name] = t
	}

	staticFS, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, err
	}

	return &Server{
		cfg:     cfg,
		store:   st,
		pub:     pub,
		cfgPath: cfgPath,
		dataDir: filepath.Dir(cfgPath),
		csrf:    token,
		pages:   pages,
		static:  http.FileServer(http.FS(staticFS)),
	}, nil
}

func (s *Server) CSRF() string { return s.csrf }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", s.static))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /", s.handleCompose)
	mux.HandleFunc("POST /send", s.handleSend)
	mux.HandleFunc("GET /history", s.handleHistory)
	mux.HandleFunc("GET /alerts/{id}", s.handleDetail)
	mux.HandleFunc("POST /alerts/{id}/resend", s.handleResend)
	mux.HandleFunc("GET /audiences", s.handleAudiences)
	mux.HandleFunc("POST /audiences", s.handleCreateAudience)
	mux.HandleFunc("POST /audiences/{id}/delete", s.handleDeleteAudience)
	mux.HandleFunc("POST /audiences/{id}/test", s.handleTestAudience)
	mux.HandleFunc("GET /settings", s.handleSettings)
	mux.HandleFunc("POST /settings", s.handleSaveSettings)

	return s.hostGuard(mux)
}

func (s *Server) Run(ctx context.Context, openBrowser, allowNonLoopback bool) error {
	host, _, err := net.SplitHostPort(s.cfg.Server.Addr)
	if err != nil {
		return fmt.Errorf("invalid address %q: %w", s.cfg.Server.Addr, err)
	}
	if !isLoopback(host) && !allowNonLoopback {
		return fmt.Errorf("refusing to bind %q: only loopback addresses are allowed without --allow-non-loopback", host)
	}

	ln, err := net.Listen("tcp", s.cfg.Server.Addr)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	if openBrowser {
		go func() {
			time.Sleep(150 * time.Millisecond)
			_ = openURL("http://" + s.cfg.Server.Addr + "/")
		}()
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) hostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r.Host) {
			http.Error(w, "host not allowed", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) hostAllowed(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	if bound, _, err := net.SplitHostPort(s.cfg.Server.Addr); err == nil {
		return host == strings.ToLower(strings.Trim(bound, "[]"))
	}
	return false
}

func (s *Server) checkCSRF(r *http.Request) bool {
	got := r.PostFormValue("csrf")
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.csrf)) == 1
}

func (s *Server) render(w http.ResponseWriter, status int, page string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := s.pages[page].ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func (s *Server) handleCompose(w http.ResponseWriter, r *http.Request) {
	audiences, err := s.store.ListAudiences(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	audienceLabel := ""
	defaultSeverity := string(format.Urgent)
	if len(audiences) > 0 {
		audienceLabel = audiences[0].Label
		if audiences[0].DefaultSeverity != "" {
			defaultSeverity = audiences[0].DefaultSeverity
		}
	}
	s.render(w, http.StatusOK, "compose", composeData{
		Page:            "compose",
		CSRF:            s.csrf,
		Audiences:       audiences,
		HasAudiences:    len(audiences) > 0,
		DefaultSeverity: defaultSeverity,
		Form:            composeForm{Audience: audienceLabel, Severity: defaultSeverity},
	})
}

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	audiences, _ := s.store.ListAudiences(r.Context())

	form := composeForm{
		Severity: strings.TrimSpace(r.PostFormValue("severity")),
		Audience: strings.TrimSpace(r.PostFormValue("audience")),
		Headline: strings.TrimSpace(r.PostFormValue("headline")),
		Details:  strings.TrimSpace(r.PostFormValue("details")),
		Link:     strings.TrimSpace(r.PostFormValue("link")),
	}
	severity, err := format.ParseSeverity(form.Severity)
	if err != nil {
		s.render(w, http.StatusBadRequest, "compose", composeData{
			Page: "compose", CSRF: s.csrf, Audiences: audiences, HasAudiences: len(audiences) > 0,
			Form: form, Error: err.Error(), DefaultSeverity: string(format.Urgent),
		})
		return
	}
	if severity == format.Urgent && r.PostFormValue("confirm") != "on" {
		s.render(w, http.StatusBadRequest, "compose", composeData{
			Page: "compose", CSRF: s.csrf, Audiences: audiences, HasAudiences: len(audiences) > 0,
			Form: form, Error: "urgent alerts bypass Do Not Disturb; tick the confirmation box to send", DefaultSeverity: string(severity),
		})
		return
	}
	audience, err := s.store.GetAudienceByLabel(r.Context(), form.Audience)
	if err != nil {
		s.render(w, http.StatusBadRequest, "compose", composeData{
			Page: "compose", CSRF: s.csrf, Audiences: audiences, HasAudiences: len(audiences) > 0,
			Form: form, Error: "choose a valid audience", DefaultSeverity: string(severity),
		})
		return
	}

	alert := format.Alert{Severity: severity, Headline: form.Headline, Details: form.Details, Link: form.Link}
	if err := alert.Validate(); err != nil {
		s.render(w, http.StatusBadRequest, "compose", composeData{
			Page: "compose", CSRF: s.csrf, Audiences: audiences, HasAudiences: len(audiences) > 0,
			Form: form, Error: err.Error(), DefaultSeverity: string(severity),
		})
		return
	}

	msg := ntfy.Message{
		Title:    alert.Title(),
		Body:     alert.Body(),
		Priority: severity.Priority(),
		Tags:     []string{severity.Tag()},
		Click:    alert.Link,
	}
	res, sendErr := s.pub.Publish(r.Context(), audience.Topic, msg)

	record := store.Alert{
		Status:     "sent",
		Severity:   string(severity),
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
	if sendErr != nil {
		record.Status = "failed"
		record.Error = sendErr.Error()
		var api *ntfy.APIError
		if errors.As(sendErr, &api) {
			record.HTTPStatus = api.Status
		}
	}

	id, recordErr := s.store.RecordAlert(r.Context(), record)
	if sendErr != nil {
		s.render(w, http.StatusBadGateway, "compose", composeData{
			Page: "compose", CSRF: s.csrf, Audiences: audiences, HasAudiences: len(audiences) > 0,
			Form: form, Error: sendErr.Error(), DefaultSeverity: string(severity),
		})
		return
	}
	if recordErr != nil {
		http.Error(w, recordErr.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/alerts/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	severity := strings.TrimSpace(r.URL.Query().Get("severity"))
	alerts, err := s.store.SearchAlerts(r.Context(), query, severity, 200)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, http.StatusOK, "history", historyData{Page: "history", Alerts: alerts, Query: query, Severity: severity})
}

func (s *Server) handleDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	alert, err := s.store.GetAlert(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, http.StatusOK, "detail", detailData{Page: "history", CSRF: s.csrf, Alert: alert})
}

func (s *Server) handleResend(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	old, err := s.store.GetAlert(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	severity, err := format.ParseSeverity(old.Severity)
	if err != nil {
		http.Error(w, "stored alert has an invalid severity", http.StatusInternalServerError)
		return
	}
	msg := ntfy.Message{
		Title:    old.Title,
		Body:     old.Body,
		Priority: old.Priority,
		Tags:     []string{severity.Tag()},
		Click:    old.Link,
	}
	res, sendErr := s.pub.Publish(r.Context(), old.Topic, msg)
	record := old
	record.ID = 0
	record.Status = "sent"
	record.ResentFrom = old.ID
	record.NtfyID = res.ID
	record.HTTPStatus = res.HTTPStatus
	record.Error = ""
	if sendErr != nil {
		record.Status = "failed"
		record.Error = sendErr.Error()
		var api *ntfy.APIError
		if errors.As(sendErr, &api) {
			record.HTTPStatus = api.Status
		}
	}
	newID, recordErr := s.store.RecordAlert(r.Context(), record)
	if sendErr != nil {
		s.render(w, http.StatusBadGateway, "detail", detailData{Page: "history", CSRF: s.csrf, Alert: old})
		return
	}
	if recordErr != nil {
		http.Error(w, recordErr.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/alerts/"+strconv.FormatInt(newID, 10), http.StatusSeeOther)
}

func (s *Server) handleAudiences(w http.ResponseWriter, r *http.Request) {
	audiences, err := s.store.ListAudiences(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, http.StatusOK, "audiences", audiencesData{Page: "audiences", CSRF: s.csrf, Audiences: audiences})
}

func (s *Server) handleCreateAudience(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	label := strings.TrimSpace(r.PostFormValue("label"))
	topic := strings.TrimSpace(r.PostFormValue("topic"))
	severity := strings.TrimSpace(r.PostFormValue("default_severity"))
	if severity == "" {
		severity = string(format.Urgent)
	}
	if label == "" {
		s.audiencesError(w, r, http.StatusBadRequest, "label is required")
		return
	}
	if topic == "" {
		generated, err := ntfy.GenerateTopic("squawk-")
		if err != nil {
			s.audiencesError(w, r, http.StatusInternalServerError, err.Error())
			return
		}
		topic = generated
	}
	if err := ntfy.ValidateTopic(topic); err != nil {
		s.audiencesError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := format.ParseSeverity(severity); err != nil {
		s.audiencesError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	_, err := s.store.CreateAudience(r.Context(), store.Audience{Label: label, Topic: topic, DefaultSeverity: severity})
	if errors.Is(err, store.ErrConflict) {
		s.audiencesError(w, r, http.StatusConflict, "an audience with that label or topic already exists")
		return
	}
	if err != nil {
		s.audiencesError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	http.Redirect(w, r, "/audiences", http.StatusSeeOther)
}

func (s *Server) handleDeleteAudience(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.store.DeleteAudience(r.Context(), id); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.audiencesError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	http.Redirect(w, r, "/audiences", http.StatusSeeOther)
}

func (s *Server) handleTestAudience(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	audience, err := s.store.GetAudience(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.audiencesError(w, r, http.StatusInternalServerError, err.Error())
		return
	}

	alert := format.Alert{Severity: format.Info, Headline: "Squawk test", Details: "If you can read this, the topic works."}
	msg := ntfy.Message{Title: alert.Title(), Body: alert.Body(), Priority: format.Info.Priority(), Tags: []string{format.Info.Tag()}}
	res, sendErr := s.pub.Publish(r.Context(), audience.Topic, msg)
	record := store.Alert{
		Status: "sent", Severity: string(format.Info), Audience: audience.Label, Topic: audience.Topic,
		Title: msg.Title, Body: msg.Body, Line: alert.Line(), Priority: msg.Priority,
		NtfyID: res.ID, HTTPStatus: res.HTTPStatus,
	}
	if sendErr != nil {
		record.Status = "failed"
		record.Error = sendErr.Error()
	}
	if _, err := s.store.RecordAlert(r.Context(), record); err != nil {
		s.audiencesError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	if sendErr != nil {
		s.audiencesError(w, r, http.StatusBadGateway, "test failed: "+sendErr.Error())
		return
	}
	s.audiencesNotice(w, r, "Test alert sent to "+audience.Label)
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusOK, "settings", settingsData{
		Page:        "settings",
		CSRF:        s.csrf,
		BaseURL:     s.cfg.BaseURL(),
		TokenStatus: s.cfg.RedactedToken(),
		Addr:        s.cfg.Server.Addr,
		OpenBrowser: s.cfg.Server.OpenBrowser,
		DataDir:     s.dataDir,
		ConfigPath:  s.cfgPath,
		Version:     version.Version,
	})
}

func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(r) {
		http.Error(w, "invalid CSRF token", http.StatusForbidden)
		return
	}
	data := settingsData{
		Page: "settings", CSRF: s.csrf, DataDir: s.dataDir, ConfigPath: s.cfgPath, Version: version.Version,
	}

	baseURL := strings.TrimSpace(r.PostFormValue("base_url"))
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || ntfy.ValidateBaseURL(baseURL) != nil {
		data.Error = "base URL must be an https URL (plain http is allowed only for localhost)"
		data.BaseURL, data.TokenStatus = baseURL, s.cfg.RedactedToken()
		data.Addr, data.OpenBrowser = s.cfg.Server.Addr, s.cfg.Server.OpenBrowser
		s.render(w, http.StatusBadRequest, "settings", data)
		return
	}
	addr := strings.TrimSpace(r.PostFormValue("addr"))
	if host, port, err := net.SplitHostPort(addr); err != nil || host == "" || port == "" {
		data.Error = "bind address must be host:port"
		data.BaseURL, data.TokenStatus = s.cfg.BaseURL(), s.cfg.RedactedToken()
		data.Addr, data.OpenBrowser = addr, s.cfg.Server.OpenBrowser
		s.render(w, http.StatusBadRequest, "settings", data)
		return
	}

	oldBase := s.cfg.Ntfy.BaseURL
	oldAddr := s.cfg.Server.Addr
	s.cfg.Ntfy.BaseURL = baseURL
	s.cfg.Server.Addr = addr
	s.cfg.Server.OpenBrowser = r.PostFormValue("open_browser") == "on"
	if token := r.PostFormValue("token"); token != "" {
		s.cfg.Ntfy.Token = token
	}
	if r.PostFormValue("clear_token") == "1" {
		s.cfg.Ntfy.Token = ""
	}

	if err := s.cfg.Save(s.cfgPath); err != nil {
		s.cfg.Ntfy.BaseURL = oldBase
		s.cfg.Server.Addr = oldAddr
		data.Error = "could not save config: " + err.Error()
		data.BaseURL, data.TokenStatus = s.cfg.BaseURL(), s.cfg.RedactedToken()
		data.Addr, data.OpenBrowser = s.cfg.Server.Addr, s.cfg.Server.OpenBrowser
		s.render(w, http.StatusInternalServerError, "settings", data)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (s *Server) audiencesError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	audiences, _ := s.store.ListAudiences(r.Context())
	s.render(w, status, "audiences", audiencesData{Page: "audiences", CSRF: s.csrf, Audiences: audiences, Error: msg})
}

func (s *Server) audiencesNotice(w http.ResponseWriter, r *http.Request, msg string) {
	audiences, _ := s.store.ListAudiences(r.Context())
	s.render(w, http.StatusOK, "audiences", audiencesData{Page: "audiences", CSRF: s.csrf, Audiences: audiences, Notice: msg})
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
