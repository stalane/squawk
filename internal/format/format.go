package format

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	MaxMessageBytes  = 4096
	MaxHeadlineRunes = 120
)

type Severity string

const (
	Urgent  Severity = "urgent"
	Warning Severity = "warning"
	Info    Severity = "info"
	Funny   Severity = "funny"
)

func ParseSeverity(s string) (Severity, error) {
	switch Severity(strings.ToLower(strings.TrimSpace(s))) {
	case Urgent:
		return Urgent, nil
	case Warning:
		return Warning, nil
	case Info:
		return Info, nil
	case Funny:
		return Funny, nil
	case "seagull":
		return Funny, nil
	default:
		return "", fmt.Errorf("unknown severity %q: use urgent, warning, info or funny", s)
	}
}

func (s Severity) Emoji() string {
	switch s {
	case Urgent:
		return "🚨"
	case Warning:
		return "⚠️"
	case Info:
		return "ℹ️"
	case Funny:
		return "😂"
	default:
		return ""
	}
}

func (s Severity) Priority() int {
	switch s {
	case Urgent:
		return 5
	case Warning:
		return 4
	case Info:
		return 3
	case Funny:
		return 3
	default:
		return 0
	}
}

func (s Severity) Tag() string {
	switch s {
	case Urgent:
		return "urgent"
	case Warning:
		return "advisory"
	case Info:
		return "notice"
	case Funny:
		return "funny"
	default:
		return ""
	}
}

func (s Severity) Label() string {
	switch s {
	case Urgent:
		return "Urgent"
	case Warning:
		return "Warning"
	case Info:
		return "Info"
	case Funny:
		return "Funny"
	default:
		return ""
	}
}

type Alert struct {
	Severity Severity
	Headline string
	Details  string
	Link     string
}

func (a Alert) Validate() error {
	if _, err := ParseSeverity(string(a.Severity)); err != nil {
		return err
	}
	headline := oneLine(a.Headline)
	if headline == "" {
		return errors.New("headline is required")
	}
	if utf8.RuneCountInString(headline) > MaxHeadlineRunes {
		return fmt.Errorf("headline must be at most %d characters", MaxHeadlineRunes)
	}
	if a.Link != "" {
		u, err := url.Parse(a.Link)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("link must be an http or https URL")
		}
	}
	if a.Bytes() > MaxMessageBytes {
		return fmt.Errorf("alert is %d bytes; the limit is %d", a.Bytes(), MaxMessageBytes)
	}
	return nil
}

func (a Alert) Title() string {
	return a.Severity.Emoji() + " " + oneLine(a.Headline)
}

func (a Alert) Body() string {
	body := strings.TrimSpace(a.Details)
	if body == "" {
		if a.Severity == Funny {
			body = "Squawk!"
		} else {
			body = "No further details."
		}
	}
	if a.Link == "" {
		return body
	}
	return body + "\n\n" + a.Link
}

func (a Alert) Line() string {
	parts := []string{a.Severity.Emoji() + " " + oneLine(a.Headline)}
	if details := oneLine(a.Details); details != "" {
		parts = append(parts, details)
	}
	if a.Link != "" {
		parts = append(parts, a.Link)
	}
	return strings.Join(parts, " -- ")
}

func (a Alert) Bytes() int {
	return len(a.Line())
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
