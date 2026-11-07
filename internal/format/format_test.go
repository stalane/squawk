package format_test

import (
	"strings"
	"testing"

	"github.com/stalane/squawk/internal/format"
)

func TestParseSeverity(t *testing.T) {
	cases := []struct {
		in    string
		want  format.Severity
		emoji string
		prio  int
		tag   string
	}{
		{"urgent", format.Urgent, "🚨", 5, "urgent"},
		{"warning", format.Warning, "⚠️", 4, "advisory"},
		{"info", format.Info, "ℹ️", 3, "notice"},
		{"seagull", format.Seagull, "🐦", 3, "seagull"},
		{"URGENT", format.Urgent, "🚨", 5, "urgent"},
		{" warning ", format.Warning, "⚠️", 4, "advisory"},
	}
	for _, c := range cases {
		got, err := format.ParseSeverity(c.in)
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if got != c.want || got.Emoji() != c.emoji || got.Priority() != c.prio || got.Tag() != c.tag {
			t.Fatalf("%q: got %v/%q/%d/%q", c.in, got, got.Emoji(), got.Priority(), got.Tag())
		}
	}
	if _, err := format.ParseSeverity("critical"); err == nil {
		t.Fatal("unknown severity must fail")
	}
	if _, err := format.ParseSeverity(""); err == nil {
		t.Fatal("empty severity must fail")
	}
}

func TestLineAndSplit(t *testing.T) {
	a := format.Alert{
		Severity: format.Urgent,
		Headline: "Crypto is being stolen",
		Details:  "Do not use Safari",
		Link:     "https://example.com/x",
	}
	if got := a.Title(); got != "🚨 Crypto is being stolen" {
		t.Fatalf("title %q", got)
	}
	if got := a.Body(); got != "Do not use Safari\n\nhttps://example.com/x" {
		t.Fatalf("body %q", got)
	}
	if got := a.Line(); got != "🚨 Crypto is being stolen -- Do not use Safari -- https://example.com/x" {
		t.Fatalf("line %q", got)
	}
}

func TestLineWithoutLink(t *testing.T) {
	a := format.Alert{Severity: format.Info, Headline: "Heads up", Details: "All calm"}
	if got := a.Line(); got != "ℹ️ Heads up -- All calm" {
		t.Fatalf("line %q", got)
	}
	if got := a.Body(); got != "All calm" {
		t.Fatalf("body %q", got)
	}
}

func TestValidateUTF8ByteLimit(t *testing.T) {
	a := format.Alert{Severity: format.Urgent, Headline: "x", Details: strings.Repeat("🚨", 1100)}
	if a.Bytes() <= 4096 {
		t.Fatalf("fixture must exceed the limit, got %d", a.Bytes())
	}
	if err := a.Validate(); err == nil {
		t.Fatal("over-limit alert must be rejected")
	}
}

func TestValidateRequiredFields(t *testing.T) {
	cases := []format.Alert{
		{Severity: format.Urgent, Headline: "", Details: "d"},
		{Severity: "nope", Headline: "h", Details: "d"},
		{Severity: format.Info, Headline: "h", Details: "d", Link: "not a url"},
		{Severity: format.Info, Headline: strings.Repeat("a", 121), Details: "d"},
	}
	for i, a := range cases {
		if err := a.Validate(); err == nil {
			t.Fatalf("case %d must fail: %+v", i, a)
		}
	}
}

func TestLineWithoutDetails(t *testing.T) {
	a := format.Alert{Severity: format.Info, Headline: "Heads up"}
	if err := a.Validate(); err != nil {
		t.Fatalf("details must be optional: %v", err)
	}
	if got := a.Line(); got != "ℹ️ Heads up" {
		t.Fatalf("line %q", got)
	}
	if got := a.Body(); got != "No further details." {
		t.Fatalf("body %q", got)
	}
}

func TestLineWithLinkButNoDetails(t *testing.T) {
	a := format.Alert{Severity: format.Seagull, Headline: "Mine", Link: "https://e.x"}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := a.Line(); got != "🐦 Mine -- https://e.x" {
		t.Fatalf("line %q", got)
	}
	if got := a.Body(); got != "Squawk!\n\nhttps://e.x" {
		t.Fatalf("body %q", got)
	}
}

func TestValidateStripsHeadlineNewlines(t *testing.T) {
	a := format.Alert{Severity: format.Info, Headline: "one\ntwo", Details: "body"}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(a.Title(), "\n") {
		t.Fatal("title must be single-line")
	}
	if strings.Contains(a.Line(), "\n") {
		t.Fatal("line must be single-line")
	}
}

func TestValidateMultilineDetails(t *testing.T) {
	a := format.Alert{Severity: format.Warning, Headline: "h", Details: "line one\nline two"}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	if a.Body() != "line one\nline two" {
		t.Fatalf("body %q", a.Body())
	}
	if strings.Contains(a.Line(), "\n") {
		t.Fatalf("line must be single-line: %q", a.Line())
	}
}

func TestSeverityTagsAreNotEmojiShortCodes(t *testing.T) {
	emojiCodes := map[string]bool{
		"rotating_light": true, "warning": true, "information_source": true,
		"bird": true, "skull": true, "triangular_flag_on_post": true,
	}
	for _, sev := range []format.Severity{format.Urgent, format.Warning, format.Info, format.Seagull} {
		if emojiCodes[sev.Tag()] {
			t.Fatalf("%s tag %q is an ntfy emoji short code; it would duplicate the title emoji", sev, sev.Tag())
		}
	}
}
