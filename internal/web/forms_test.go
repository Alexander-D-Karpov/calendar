package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/ratelimit"
)

func TestFormSetError(t *testing.T) {
	f := NewForm(url.Values{"email": {" a@b.test "}})
	if f.Get("email") != "a@b.test" {
		t.Fatalf("Get = %q", f.Get("email"))
	}
	var ve domain.ValidationError
	ve.Add("password", "must be at least 10 characters")
	if !f.SetError(ve.Err()) || f.Err("password") != "Must be at least 10 characters." {
		t.Fatalf("validation = %v", f.Errors)
	}
	cases := map[error]string{
		auth.ErrInvalidCredentials:                     "Incorrect email or password.",
		auth.ErrDisabled:                               "This account is disabled.",
		&ratelimit.Error{RetryAfter: 90 * time.Second}: "Too many attempts. Try again in 2 minutes.",
		&ratelimit.Error{RetryAfter: 5 * time.Second}:  "Too many attempts. Try again in 5 seconds.",
	}
	for err, want := range cases {
		f := NewForm(nil)
		if !f.SetError(err) || f.Error != want {
			t.Errorf("SetError(%v) = %q", err, f.Error)
		}
	}
	if NewForm(nil).SetError(domain.ErrNotFound) {
		t.Error("unknown errors must not be handled by forms")
	}
}

func TestSafeNext(t *testing.T) {
	cases := map[string]string{
		"":                       "/",
		"/":                      "/",
		"/settings/tokens":       "/settings/tokens",
		"/cal/week?d=2026-09-10": "/cal/week?d=2026-09-10",
		"//evil.test":            "/",
		"/\\evil.test":           "/",
		"https://evil.test":      "/",
		"javascript:alert(1)":    "/",
		"settings":               "/",
		"/a\r\nSet-Cookie: x":    "/",
	}
	for in, want := range cases {
		if got := SafeNext(in); got != want {
			t.Errorf("SafeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDescribeUA(t *testing.T) {
	cases := map[string]string{
		"Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0":                                "Firefox on Linux",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/128.0 Safari/537.36":         "Chrome on macOS",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Version/17.0 Safari/604.1": "Safari on iOS",
		"Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 Chrome/128.0 Mobile Safari/537.36":                  "Chrome on Android",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/128.0 Safari/537.36 Edg/128.0":     "Edge on Windows",
		"curl/8.9.1":   "curl",
		"calendar-cli": "Calendar CLI",
		"":             "Unknown device",
	}
	for in, want := range cases {
		if got := describeUA(in); got != want {
			t.Errorf("describeUA(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFlash(t *testing.T) {
	keys, err := crypto.NewKeyRing(map[uint32][]byte{1: bytes.Repeat([]byte{1}, crypto.KeySize)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	f := flasher{keys: keys}
	rec := httptest.NewRecorder()
	f.Set(rec, "ok", "Saved; with = signs", "")
	cookie := rec.Result().Cookies()[0]

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(cookie)
	out := httptest.NewRecorder()
	got := f.Pop(out, req)
	if got == nil || got.Kind != "ok" || got.Text != "Saved; with = signs" {
		t.Fatalf("Pop = %+v", got)
	}
	if c := out.Result().Cookies(); len(c) != 1 || c[0].MaxAge != -1 {
		t.Fatalf("Pop must clear the cookie, got %+v", c)
	}

	tampered := *cookie
	tampered.Value = strings.Replace(cookie.Value, cookie.Value[:4], "AAAA", 1)
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&tampered)
	if f.Pop(httptest.NewRecorder(), req) != nil {
		t.Fatal("tampered flash must be rejected")
	}
	if f.Pop(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil)) != nil {
		t.Fatal("missing flash must be nil")
	}
}
