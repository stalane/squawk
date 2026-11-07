package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrConflict = errors.New("already exists")
	ErrNotFound = errors.New("not found")
)

const schema = `
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
  status TEXT NOT NULL,
  severity TEXT NOT NULL,
  audience TEXT NOT NULL,
  topic TEXT NOT NULL,
  title TEXT NOT NULL,
  body TEXT NOT NULL,
  line TEXT NOT NULL,
  link TEXT NOT NULL DEFAULT '',
  priority INTEGER NOT NULL,
  ntfy_id TEXT NOT NULL DEFAULT '',
  http_status INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',
  resent_from INTEGER
);

CREATE INDEX IF NOT EXISTS idx_alerts_created_at ON alerts(created_at DESC);
`

const alertColumns = `id, created_at, sent_at, status, severity, audience, topic, title, body, line, link, priority, ntfy_id, http_status, error, resent_from`

type Audience struct {
	ID              int64
	Label           string
	Topic           string
	DefaultSeverity string
	CreatedAt       time.Time
}

type Alert struct {
	ID         int64
	CreatedAt  time.Time
	SentAt     time.Time
	Status     string
	Severity   string
	Audience   string
	Topic      string
	Title      string
	Body       string
	Line       string
	Link       string
	Priority   int
	NtfyID     string
	HTTPStatus int
	Error      string
	ResentFrom int64
}

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, err
		}
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) CreateAudience(ctx context.Context, a Audience) (int64, error) {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	if a.DefaultSeverity == "" {
		a.DefaultSeverity = "urgent"
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO audiences (label, topic, default_severity, created_at) VALUES (?, ?, ?, ?)`,
		a.Label, a.Topic, a.DefaultSeverity, formatTime(a.CreatedAt))
	if err != nil {
		return 0, mapConstraint(err)
	}
	return res.LastInsertId()
}

func (s *Store) ListAudiences(ctx context.Context) ([]Audience, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, label, topic, default_severity, created_at FROM audiences ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Audience
	for rows.Next() {
		var a Audience
		var created string
		if err := rows.Scan(&a.ID, &a.Label, &a.Topic, &a.DefaultSeverity, &created); err != nil {
			return nil, err
		}
		a.CreatedAt = parseTime(created)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) GetAudience(ctx context.Context, id int64) (Audience, error) {
	return s.getAudience(ctx, `SELECT id, label, topic, default_severity, created_at FROM audiences WHERE id = ?`, id)
}

func (s *Store) GetAudienceByLabel(ctx context.Context, label string) (Audience, error) {
	return s.getAudience(ctx, `SELECT id, label, topic, default_severity, created_at FROM audiences WHERE label = ?`, label)
}

func (s *Store) getAudience(ctx context.Context, query string, arg any) (Audience, error) {
	var a Audience
	var created string
	err := s.db.QueryRowContext(ctx, query, arg).Scan(&a.ID, &a.Label, &a.Topic, &a.DefaultSeverity, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Audience{}, ErrNotFound
	}
	if err != nil {
		return Audience{}, err
	}
	a.CreatedAt = parseTime(created)
	return a, nil
}

func (s *Store) UpdateAudience(ctx context.Context, a Audience) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE audiences SET label = ?, topic = ?, default_severity = ? WHERE id = ?`,
		a.Label, a.Topic, a.DefaultSeverity, a.ID)
	if err != nil {
		return mapConstraint(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteAudience(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM audiences WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RecordAlert(ctx context.Context, a Alert) (int64, error) {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	if a.Status == "" {
		a.Status = "sent"
	}
	var sent *string
	if !a.SentAt.IsZero() {
		v := formatTime(a.SentAt)
		sent = &v
	} else if a.Status == "sent" {
		v := formatTime(a.CreatedAt)
		sent = &v
	}
	var resent *int64
	if a.ResentFrom != 0 {
		resent = &a.ResentFrom
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO alerts (created_at, sent_at, status, severity, audience, topic, title, body, line, link, priority, ntfy_id, http_status, error, resent_from)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		formatTime(a.CreatedAt), sent, a.Status, a.Severity, a.Audience, a.Topic,
		a.Title, a.Body, a.Line, a.Link, a.Priority, a.NtfyID, a.HTTPStatus, a.Error, resent)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListAlerts(ctx context.Context, limit int) ([]Alert, error) {
	return s.SearchAlerts(ctx, "", "", limit)
}

func (s *Store) SearchAlerts(ctx context.Context, query, severity string, limit int) ([]Alert, error) {
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT ` + alertColumns + ` FROM alerts WHERE 1=1`
	var args []any
	if severity != "" {
		q += ` AND severity = ?`
		args = append(args, severity)
	}
	if query != "" {
		like := "%" + query + "%"
		q += ` AND (title LIKE ? OR body LIKE ? OR line LIKE ? OR audience LIKE ?)`
		args = append(args, like, like, like, like)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Alert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) GetAlert(ctx context.Context, id int64) (Alert, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+alertColumns+` FROM alerts WHERE id = ?`, id)
	a, err := scanAlert(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Alert{}, ErrNotFound
	}
	return a, err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanAlert(sc scanner) (Alert, error) {
	var a Alert
	var created string
	var sent sql.NullString
	var resent sql.NullInt64
	err := sc.Scan(&a.ID, &created, &sent, &a.Status, &a.Severity, &a.Audience, &a.Topic,
		&a.Title, &a.Body, &a.Line, &a.Link, &a.Priority, &a.NtfyID, &a.HTTPStatus, &a.Error, &resent)
	if err != nil {
		return Alert{}, err
	}
	a.CreatedAt = parseTime(created)
	if sent.Valid {
		a.SentAt = parseTime(sent.String)
	}
	if resent.Valid {
		a.ResentFrom = resent.Int64
	}
	return a, nil
}

func mapConstraint(err error) error {
	if strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return err
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
