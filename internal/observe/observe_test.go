package observe

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"

	"github.com/Alexander-D-Karpov/calendar/internal/httpx"
)

type captured struct {
	mu     sync.Mutex
	events []*sentry.Event
}

func (c *captured) Configure(sentry.ClientOptions)        {}
func (c *captured) Flush(time.Duration) bool              { return true }
func (c *captured) FlushWithContext(context.Context) bool { return true }
func (c *captured) Close()                                {}
func (c *captured) SendEvent(e *sentry.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *captured) only(t *testing.T) *sentry.Event {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.events) != 1 {
		t.Fatalf("captured %d events, want 1", len(c.events))
	}
	return c.events[0]
}

func newTestClient(t *testing.T) (*sentry.Client, *captured) {
	t.Helper()
	tr := &captured{}
	opts := clientOptions(Options{DSN: "https://key@o0.ingest.test/1", Environment: "test", Release: "calendar@test"})
	opts.Transport = tr
	opts.Integrations = func([]sentry.Integration) []sentry.Integration { return nil }
	c, err := sentry.NewClient(opts)
	if err != nil {
		t.Fatal(err)
	}
	return c, tr
}

func useGlobal(t *testing.T, c *sentry.Client) {
	t.Helper()
	sentry.CurrentHub().BindClient(c)
	enabled.Store(true)
	t.Cleanup(func() {
		sentry.CurrentHub().BindClient(nil)
		enabled.Store(false)
	})
}

func newestFrame(t *testing.T, st *sentry.Stacktrace) sentry.Frame {
	t.Helper()
	if st == nil || len(st.Frames) == 0 {
		t.Fatal("missing stacktrace")
	}
	return st.Frames[len(st.Frames)-1]
}

func TestScrub(t *testing.T) {
	e := sentry.NewEvent()
	e.Message = "fetch https://u:p@feed.test/cal.ics?token=secret failed, Authorization: Bearer abc.def"
	e.Exception = []sentry.Exception{{Type: "x", Value: "token cal_ab12_Zx9-secret rejected"}}
	e.Contexts["extra"] = sentry.Context{"url": "https://x.test/a?b=c", "n": 3}
	e.Tags["path"] = "/s/AbCdEf0123456789xyz/og.png"
	e.User = sentry.User{ID: "u1", Email: "a@b.test", IPAddress: "1.2.3.4"}
	e.Breadcrumbs = []*sentry.Breadcrumb{{Message: "GET /s/AbCdEf0123456789xyz", Data: map[string]any{"url": "https://g.test/x?syncToken=abc"}}}
	e.Request = &sentry.Request{
		URL:         "https://cal.test/s/AbCdEf0123456789xyz/og.png?v=2",
		QueryString: "v=2",
		Cookies:     "__Host-sid=abc",
		Data:        "password=hunter2",
		Env:         map[string]string{"REMOTE_ADDR": "1.2.3.4"},
		Headers: map[string]string{
			"Authorization":        "Bearer cal_ab12_secret",
			"Cookie":               "__Host-sid=abc",
			"X-Goog-Channel-Token": "chan-9f3a",
			"User-Agent":           "Telegram",
			"referer":              "https://cal.test/reset?token=abc",
		},
	}

	Scrub(e)
	blob := fmt.Sprintf("%+v %+v %+v %+v %+v", e.Message, e.Exception, e.Contexts, e.Tags, *e.Request)
	for _, b := range e.Breadcrumbs {
		blob += fmt.Sprintf("%+v", *b)
	}
	for _, leak := range []string{"secret", "abc.def", "u:p@", "b=c", "AbCdEf0123456789xyz", "hunter2", "__Host-sid", "chan-9f3a", "1.2.3.4", "syncToken", "a@b.test"} {
		if strings.Contains(blob, leak) {
			t.Errorf("scrubbed event leaks %q: %s", leak, blob)
		}
	}
	if e.Request.URL != "https://cal.test/s/:redacted/og.png" {
		t.Errorf("URL = %q", e.Request.URL)
	}
	if e.Request.Headers["User-Agent"] != "Telegram" || e.Request.Headers["Referer"] != "https://cal.test/reset" {
		t.Errorf("headers = %v", e.Request.Headers)
	}
	if e.User.ID != "u1" || e.Contexts["extra"]["n"] != 3 {
		t.Errorf("user = %+v contexts = %v", e.User, e.Contexts)
	}
}

func TestSlogHandlerCapturesErrors(t *testing.T) {
	client, tr := newTestClient(t)
	hub := sentry.NewHub(client, sentry.NewScope())
	ctx := sentry.SetHubOnContext(context.Background(), hub)
	logger := slog.New(NewSlogHandler(nil)).With("component", "sync")

	base := errors.New("boom")
	logger.DebugContext(ctx, "noise")
	logger.InfoContext(ctx, "pulling", "binding", "b1")
	logger.ErrorContext(ctx, "sync failed",
		"err", fmt.Errorf("pull: %w", base),
		"binding", "b1",
		"url", "https://www.googleapis.test/cal?syncToken=abc",
	)

	e := tr.only(t)
	if e.Message != "sync failed" || e.Level != sentry.LevelError || e.Tags["component"] != "sync" {
		t.Fatalf("event = %q %q %v", e.Message, e.Level, e.Tags)
	}
	if len(e.Exception) != 2 || e.Exception[0].Value != "boom" || e.Exception[1].Value != "pull: boom" {
		t.Fatalf("exceptions = %+v", e.Exception)
	}
	if fn := newestFrame(t, e.Exception[1].Stacktrace).Function; !strings.HasPrefix(fn, "TestSlogHandlerCapturesErrors") {
		t.Fatalf("newest frame = %q", fn)
	}
	attrs := e.Contexts["log"]
	if attrs["binding"] != "b1" || attrs["url"] != "https://www.googleapis.test/cal?[redacted]" {
		t.Fatalf("log context = %v", attrs)
	}
	if len(e.Breadcrumbs) != 1 || e.Breadcrumbs[0].Message != "pulling" {
		t.Fatalf("breadcrumbs = %+v", e.Breadcrumbs)
	}
}

func TestSlogHandlerWithoutClient(t *testing.T) {
	logger := slog.New(NewSlogHandler(nil))
	logger.Error("nobody listens", "err", errors.New("x"))
	if NewSlogHandler(nil).Enabled(context.Background(), slog.LevelInfo) {
		t.Fatal("info without a request hub must be disabled")
	}
}

func explode() {
	panic("kaboom")
}

func TestMiddlewareCapturesPanic(t *testing.T) {
	client, tr := newTestClient(t)
	useGlobal(t, client)
	logger := slog.New(NewSlogHandler(nil))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /s/{token}", func(http.ResponseWriter, *http.Request) { explode() })
	h := httpx.Chain(mux, httpx.Route(mux), Middleware, httpx.Recover(logger))

	req := httptest.NewRequest(http.MethodGet, "/s/AbCdEf0123456789xyz?x=1", nil)
	req.Header.Set("Cookie", "__Host-sid=abc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d", rec.Code)
	}

	e := tr.only(t)
	if len(e.Exception) != 1 || e.Exception[0].Type != "panic" || e.Exception[0].Value != "kaboom" {
		t.Fatalf("exception = %+v", e.Exception)
	}
	if fn := newestFrame(t, e.Exception[0].Stacktrace).Function; fn != "explode" {
		t.Fatalf("newest frame = %q, want explode", fn)
	}
	if e.Tags["route"] != "GET /s/{token}" {
		t.Fatalf("tags = %v", e.Tags)
	}
	if e.Request == nil || e.Request.URL != "http://example.com/s/:redacted" || e.Request.Cookies != "" {
		t.Fatalf("request = %+v", e.Request)
	}
}
