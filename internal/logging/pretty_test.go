package logging

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/term"
)

var ts = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

func emit(t *testing.T, h slog.Handler, level slog.Level, msg string, attrs ...slog.Attr) {
	t.Helper()
	r := slog.NewRecord(ts, level, msg, 0)
	r.AddAttrs(attrs...)
	if err := h.Handle(context.Background(), r); err != nil {
		t.Fatal(err)
	}
}

func expect(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
}

func TestPrettyLine(t *testing.T) {
	var buf bytes.Buffer
	h := NewPrettyHandler(&buf, PrettyOptions{})
	emit(t, h, slog.LevelInfo, "http request",
		slog.String("method", "GET"),
		slog.String("path", "/cal"),
		slog.Int("status", 200),
		slog.Duration("duration", 1500*time.Microsecond),
	)
	expect(t, buf.String(), "12:00:00.000 INFO  http request method=GET path=/cal status=200 duration=1.5ms\n")
}

func TestPrettyGroups(t *testing.T) {
	var buf bytes.Buffer
	h := NewPrettyHandler(&buf, PrettyOptions{}).
		WithAttrs([]slog.Attr{slog.String("component", "sync")}).
		WithGroup("req")
	emit(t, h, slog.LevelWarn, "slow",
		slog.Int("id", 1),
		slog.Group("user", slog.String("name", "sasha")),
	)
	expect(t, buf.String(), "12:00:00.000 WARN  slow component=sync req.id=1 req.user.name=sasha\n")
}

func TestPrettyGroupThenAttrs(t *testing.T) {
	var buf bytes.Buffer
	h := NewPrettyHandler(&buf, PrettyOptions{}).WithGroup("g").WithAttrs([]slog.Attr{slog.Int("a", 1)})
	emit(t, h, slog.LevelInfo, "m")
	expect(t, buf.String(), "12:00:00.000 INFO  m g.a=1\n")
}

func TestPrettyHandlersDoNotShareFields(t *testing.T) {
	var buf bytes.Buffer
	base := NewPrettyHandler(&buf, PrettyOptions{}).WithAttrs([]slog.Attr{slog.Int("a", 1)})
	left := base.WithAttrs([]slog.Attr{slog.Int("b", 2)})
	right := base.WithAttrs([]slog.Attr{slog.Int("c", 3)})
	emit(t, left, slog.LevelInfo, "l")
	emit(t, right, slog.LevelInfo, "r")
	expect(t, buf.String(), "12:00:00.000 INFO  l a=1 b=2\n12:00:00.000 INFO  r a=1 c=3\n")
}

func TestPrettyQuotingAndErrors(t *testing.T) {
	var buf bytes.Buffer
	h := NewPrettyHandler(&buf, PrettyOptions{})
	emit(t, h, slog.LevelError, "fetch failed",
		slog.String("q", "hello world"),
		slog.String("e", ""),
		slog.Any("err", errors.New("no such host")),
	)
	expect(t, buf.String(), `12:00:00.000 ERROR fetch failed q="hello world" e="" err="no such host"`+"\n")
}

func TestPrettyMultiline(t *testing.T) {
	var buf bytes.Buffer
	h := NewPrettyHandler(&buf, PrettyOptions{})
	emit(t, h, slog.LevelError, "panic recovered",
		slog.String("method", "GET"),
		slog.String("stack", "goroutine 1\n\tmain.go:10\n"),
	)
	expect(t, buf.String(), "12:00:00.000 ERROR panic recovered method=GET\n  stack\n    goroutine 1\n    \tmain.go:10\n")
}

func TestPrettyLevelFilter(t *testing.T) {
	h := NewPrettyHandler(&bytes.Buffer{}, PrettyOptions{Level: slog.LevelWarn})
	ctx := context.Background()
	if h.Enabled(ctx, slog.LevelInfo) {
		t.Error("info must be disabled at warn level")
	}
	if !h.Enabled(ctx, slog.LevelError) {
		t.Error("error must be enabled at warn level")
	}
}

func TestPrettyColor(t *testing.T) {
	var plain, colored bytes.Buffer
	attrs := []slog.Attr{slog.Int("status", 503), slog.Any("err", errors.New("down"))}
	emit(t, NewPrettyHandler(&plain, PrettyOptions{}), slog.LevelError, "upstream", attrs...)
	emit(t, NewPrettyHandler(&colored, PrettyOptions{Color: true}), slog.LevelError, "upstream", attrs...)
	if !strings.Contains(colored.String(), "\x1b[") {
		t.Fatal("expected ANSI codes")
	}
	expect(t, term.Strip(colored.String()), plain.String())
}

func TestNewHandlerFormats(t *testing.T) {
	var js, txt, pretty bytes.Buffer
	New(Options{Format: FormatJSON, Writer: &js}).Info("x", "k", 1)
	New(Options{Format: FormatText, Writer: &txt}).Info("x", "k", 1)
	New(Options{Format: FormatPretty, Writer: &pretty, Color: term.ColorNever}).Info("x", "k", 1)
	if !strings.HasPrefix(js.String(), "{") {
		t.Errorf("json output = %q", js.String())
	}
	if !strings.Contains(txt.String(), "msg=x") {
		t.Errorf("text output = %q", txt.String())
	}
	if !strings.Contains(pretty.String(), "INFO  x k=1") {
		t.Errorf("pretty output = %q", pretty.String())
	}
}
