package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stalane/squawk/internal/store"
)

var ctx = context.Background()

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "squawk.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestAudienceCRUDAndUniqueness(t *testing.T) {
	s := newTestStore(t)
	id, err := s.CreateAudience(ctx, store.Audience{Label: "Family", Topic: "squawk-family-abc", DefaultSeverity: "urgent"})
	if err != nil || id == 0 {
		t.Fatalf("id=%d err=%v", id, err)
	}

	if _, err := s.CreateAudience(ctx, store.Audience{Label: "Family", Topic: "squawk-other"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate label: %v", err)
	}
	if _, err := s.CreateAudience(ctx, store.Audience{Label: "Clients", Topic: "squawk-family-abc"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate topic: %v", err)
	}

	got, err := s.GetAudienceByLabel(ctx, "Family")
	if err != nil || got.Topic != "squawk-family-abc" {
		t.Fatalf("%+v %v", got, err)
	}

	got.Label = "Close family"
	if err := s.UpdateAudience(ctx, got); err != nil {
		t.Fatal(err)
	}
	listed, err := s.ListAudiences(ctx)
	if err != nil || len(listed) != 1 || listed[0].Label != "Close family" {
		t.Fatalf("%+v %v", listed, err)
	}

	if err := s.DeleteAudience(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAudience(ctx, id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted audience: %v", err)
	}
}

func TestRecordAndSearchAlerts(t *testing.T) {
	s := newTestStore(t)
	for i, sev := range []string{"urgent", "info", "urgent"} {
		_, err := s.RecordAlert(ctx, store.Alert{
			Status:   "sent",
			Severity: sev,
			Audience: "Family",
			Topic:    "squawk-family-abc",
			Title:    fmt.Sprintf("Title %d", i),
			Body:     "shared body",
			Line:     fmt.Sprintf("line %d", i),
			Priority: 5,
			NtfyID:   fmt.Sprintf("id%d", i),
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	all, err := s.ListAlerts(ctx, 10)
	if err != nil || len(all) != 3 {
		t.Fatalf("list: %d %v", len(all), err)
	}
	if all[0].NtfyID != "id2" {
		t.Fatalf("newest first, got %q", all[0].NtfyID)
	}
	if all[0].ID == 0 || all[0].CreatedAt.IsZero() {
		t.Fatalf("missing generated fields: %+v", all[0])
	}

	urgent, err := s.SearchAlerts(ctx, "", "urgent", 10)
	if err != nil || len(urgent) != 2 {
		t.Fatalf("severity filter: %d %v", len(urgent), err)
	}
	text, err := s.SearchAlerts(ctx, "shared", "", 10)
	if err != nil || len(text) != 3 {
		t.Fatalf("text filter: %d %v", len(text), err)
	}
	none, err := s.SearchAlerts(ctx, "no-such-text", "", 10)
	if err != nil || len(none) != 0 {
		t.Fatalf("empty filter: %d %v", len(none), err)
	}
}

func TestGetAlert(t *testing.T) {
	s := newTestStore(t)
	id, err := s.RecordAlert(ctx, store.Alert{Status: "failed", Severity: "info", Audience: "A", Topic: "t", Title: "T", Body: "B", Line: "L", Priority: 3, Error: "boom"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAlert(ctx, id)
	if err != nil || got.Error != "boom" || got.Status != "failed" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := s.GetAlert(ctx, id+999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing alert: %v", err)
	}
}

func TestNewestFirstUsesInsertionOrder(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.RecordAlert(ctx, store.Alert{Status: "sent", Severity: "info", Audience: "A", Topic: "t", Title: "T", Body: "B", Line: "L", Priority: 3, NtfyID: "future", CreatedAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordAlert(ctx, store.Alert{Status: "sent", Severity: "info", Audience: "A", Topic: "t", Title: "T", Body: "B", Line: "L", Priority: 3, NtfyID: "now"}); err != nil {
		t.Fatal(err)
	}
	all, err := s.ListAlerts(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if all[0].NtfyID != "now" {
		t.Fatalf("newest insert must sort first, got %q", all[0].NtfyID)
	}
}

func TestAlertAttachmentFields(t *testing.T) {
	s := newTestStore(t)
	id, err := s.RecordAlert(ctx, store.Alert{
		Status: "sent", Severity: "seagull", Audience: "A", Topic: "t",
		Title: "🐦 T", Body: "B", Line: "L", Priority: 3,
		AttachmentName: "squawk.jpg", AttachmentSize: 72101,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAlert(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.AttachmentName != "squawk.jpg" || got.AttachmentSize != 72101 {
		t.Fatalf("%+v", got)
	}
}

func TestMigratesV010Schema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE alerts (
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
	)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer s.Close()
	id, err := s.RecordAlert(ctx, store.Alert{Status: "sent", Severity: "seagull", Audience: "A", Topic: "t", Title: "T", Body: "B", Line: "L", Priority: 3, AttachmentName: "squawk.jpg", AttachmentSize: 7})
	if err != nil {
		t.Fatalf("insert after migrate: %v", err)
	}
	got, err := s.GetAlert(ctx, id)
	if err != nil || got.AttachmentName != "squawk.jpg" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestDeleteAlert(t *testing.T) {
	s := newTestStore(t)
	id, err := s.RecordAlert(ctx, store.Alert{Status: "sent", Severity: "info", Audience: "A", Topic: "t", Title: "T", Body: "B", Line: "L", Priority: 3})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAlert(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAlert(ctx, id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted alert still present: %v", err)
	}
	if err := s.DeleteAlert(ctx, id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestMigratesSeagullToFunny(t *testing.T) {
	path := filepath.Join(t.TempDir(), "squawk.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.RecordAlert(ctx, store.Alert{Status: "sent", Severity: "seagull", Audience: "A", Topic: "t", Title: "T", Body: "B", Line: "L", Priority: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAudience(ctx, store.Audience{Label: "Old", Topic: "squawk-old", DefaultSeverity: "seagull"}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	alert, err := s.GetAlert(ctx, id)
	if err != nil || alert.Severity != "funny" {
		t.Fatalf("alert severity %q err %v", alert.Severity, err)
	}
	audiences, err := s.ListAudiences(ctx)
	if err != nil || len(audiences) != 1 || audiences[0].DefaultSeverity != "funny" {
		t.Fatalf("audiences %+v err %v", audiences, err)
	}
}
