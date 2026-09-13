package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

var noop = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

func serve(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestSecurityHeadersHTTPS(t *testing.T) {
	csp := BuildCSP(mustURL(t, "https://calendar.example.com"))
	h := SecurityHeaders(SecurityOptions{CSP: csp, HSTS: true})(noop)
	rec := serve(h, httptest.NewRequest(http.MethodGet, "/", nil))
	hdr := rec.Header()

	if got := hdr.Get("Strict-Transport-Security"); !strings.HasPrefix(got, "max-age=") {
		t.Errorf("HSTS = %q", got)
	}
	got := hdr.Get("Content-Security-Policy")
	for _, want := range []string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self'",
		"connect-src 'self' wss://calendar.example.com",
		"frame-ancestors 'none'",
		"object-src 'none'",
		"base-uri 'none'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("CSP missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "unsafe-inline") || strings.Contains(got, "unsafe-eval") {
		t.Errorf("CSP must not allow unsafe sources: %q", got)
	}
	expect := map[string]string{
		"X-Content-Type-Options":     "nosniff",
		"X-Frame-Options":            "DENY",
		"Referrer-Policy":            "same-origin",
		"Cross-Origin-Opener-Policy": "same-origin",
	}
	for k, v := range expect {
		if hdr.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, hdr.Get(k), v)
		}
	}
}

func TestSecurityHeadersPlainHTTP(t *testing.T) {
	csp := BuildCSP(mustURL(t, "http://localhost:8080"))
	h := SecurityHeaders(SecurityOptions{CSP: csp})(noop)
	rec := serve(h, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS must not be set without https")
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "ws://localhost:8080") {
		t.Error("CSP must allow plain ws on http origin")
	}
}

func TestHandlerCanOverrideHeaders(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		SetCSP(w, "default-src 'none'")
		SetReferrerPolicy(w, "no-referrer")
	})
	h := SecurityHeaders(SecurityOptions{CSP: "default-src 'self'"})(inner)
	rec := serve(h, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec.Header().Get("Content-Security-Policy"); got != "default-src 'none'" {
		t.Errorf("CSP = %q", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q", got)
	}
}

func TestChainOrder(t *testing.T) {
	var order []string
	mw := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		order = append(order, "handler")
	}), mw("a"), mw("b"), mw("c"))
	serve(h, httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.Join(order, ",") != "a,b,c,handler" {
		t.Fatalf("order = %v", order)
	}
}

func TestRequestID(t *testing.T) {
	var seen string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestIDFrom(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, "abc-123")
	rec := serve(h, req)
	if seen != "abc-123" || rec.Header().Get(RequestIDHeader) != "abc-123" {
		t.Fatalf("valid id not preserved: %q", seen)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(RequestIDHeader, "bad id\nwith newline")
	serve(h, req)
	if seen == "" || strings.ContainsAny(seen, " \n") {
		t.Fatalf("invalid id not replaced: %q", seen)
	}
}

func TestLoggingRedactsAndRecords(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hi"))
	})
	h := Logging(LogOptions{Logger: logger, Redact: RedactPathSegment("/s/")})(inner)
	serve(h, httptest.NewRequest(http.MethodGet, "/s/secrettoken/og.png?v=1", nil))

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("log line: %v", err)
	}
	if line["path"] != "/s/:redacted/og.png" {
		t.Errorf("path = %v", line["path"])
	}
	if line["status"] != float64(http.StatusTeapot) {
		t.Errorf("status = %v", line["status"])
	}
	if line["bytes"] != float64(2) {
		t.Errorf("bytes = %v", line["bytes"])
	}
	if strings.Contains(buf.String(), "secrettoken") {
		t.Error("token leaked into logs")
	}
}

func TestLoggingSkip(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	h := Logging(LogOptions{
		Logger: logger,
		Skip:   func(r *http.Request) bool { return r.URL.Path == "/healthz" },
	})(noop)
	serve(h, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if buf.Len() != 0 {
		t.Fatalf("skipped request was logged: %s", buf.String())
	}
}

func TestRedactPathSegment(t *testing.T) {
	redact := RedactPathSegment("/s/")
	cases := map[string]string{
		"/s/tok":        "/s/:redacted",
		"/s/tok.ics":    "/s/:redacted.ics",
		"/s/tok/ws":     "/s/:redacted/ws",
		"/s/tok/og.png": "/s/:redacted/og.png",
		"/s/":           "/s/",
		"/cal/week":     "/cal/week",
	}
	for in, want := range cases {
		if got := redact(in); got != want {
			t.Errorf("redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRecoverWritesServerError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := Recover(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rec := serve(h, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestRecoverRepanicsAbort(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := Recover(logger)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		v := recover()
		if err, ok := v.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
			t.Fatalf("recovered %v, want ErrAbortHandler", v)
		}
	}()
	serve(h, httptest.NewRequest(http.MethodGet, "/", nil))
	t.Fatal("expected panic")
}

func TestBodyLimit(t *testing.T) {
	var readErr error
	h := FixedBodyLimit(5)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	}))
	serve(h, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("0123456789")))
	if !IsBodyTooLarge(readErr) {
		t.Fatalf("err = %v, want MaxBytesError", readErr)
	}

	serve(h, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("0123")))
	if readErr != nil {
		t.Fatalf("small body: %v", readErr)
	}
}
