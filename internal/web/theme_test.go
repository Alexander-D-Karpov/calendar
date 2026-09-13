package web

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/config"
	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
)

func themeServer(t *testing.T) http.Handler {
	t.Helper()
	cfg := &config.Config{}
	u, err := url.Parse("https://calendar.test")
	if err != nil {
		t.Fatal(err)
	}
	cfg.App.BaseURL = u
	keys, err := crypto.NewKeyRing(map[uint32][]byte{1: bytes.Repeat([]byte{7}, crypto.KeySize)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Deps{
		Config: cfg,
		Logger: slog.New(slog.DiscardHandler),
		Guard:  auth.NewGuard(&auth.Service{}, cfg.App.Origin(), nil),
		Keys:   keys,
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("POST /theme", s.Handle(s.setTheme))
	return mux
}

func postTheme(t *testing.T, h http.Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/theme", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// An anonymous viewer on a public share page can switch the theme. The share
// sets Referrer-Policy: no-referrer, so the browser sends Origin: null and no
// Referer, and the form's own "next" is the only way back.
func TestThemeOnSharePage(t *testing.T) {
	h := themeServer(t)
	w := postTheme(t, h, "theme=dark&next=%2Fs%2Ftok", map[string]string{
		"Origin":         "null",
		"Sec-Fetch-Site": "same-origin",
	})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303 (body %q)", w.Code, w.Body)
	}
	if got := w.Header().Get("Location"); got != "/s/tok" {
		t.Errorf("Location = %q, want /s/tok", got)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != themeCookie || cookies[0].Value != "dark" {
		t.Fatalf("cookies = %+v", cookies)
	}
}

func TestThemeRedirect(t *testing.T) {
	h := themeServer(t)
	same := map[string]string{"Origin": "https://calendar.test", "Sec-Fetch-Site": "same-origin"}

	withReferer := map[string]string{"Referer": "https://calendar.test/cal/week"}
	for k, v := range same {
		withReferer[k] = v
	}
	// A real Referer wins over the form's next.
	if got := postTheme(t, h, "theme=light&next=%2Fs%2Ftok", withReferer).Header().Get("Location"); got != "/cal/week" {
		t.Errorf("Location = %q, want /cal/week", got)
	}
	// Neither available: fall back to the root.
	if got := postTheme(t, h, "theme=light", same).Header().Get("Location"); got != "/" {
		t.Errorf("Location = %q, want /", got)
	}
	// next is not an open redirect.
	if got := postTheme(t, h, "theme=light&next=https%3A%2F%2Fevil.test", same).Header().Get("Location"); got != "/" {
		t.Errorf("Location = %q, want /", got)
	}
}

func TestThemeRejects(t *testing.T) {
	h := themeServer(t)
	cases := map[string]map[string]string{
		"cross site":              {"Origin": "https://evil.test", "Sec-Fetch-Site": "cross-site"},
		"null origin, no headers": {"Origin": "null"},
		"no origin, no headers":   nil,
	}
	for name, headers := range cases {
		if w := postTheme(t, h, "theme=dark", headers); w.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", name, w.Code)
		}
	}
	same := map[string]string{"Origin": "https://calendar.test", "Sec-Fetch-Site": "same-origin"}
	if w := postTheme(t, h, "theme=neon", same); w.Code == http.StatusSeeOther {
		t.Error("an unknown theme must not be accepted")
	}
}
