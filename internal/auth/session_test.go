package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
)

func TestSessionPolicy(t *testing.T) {
	p := SessionPolicy{TTL: time.Hour, AbsoluteTTL: 3 * time.Hour, Secure: true}
	exp, abs := p.NewExpiry(t0)
	if !exp.Equal(t0.Add(time.Hour)) || !abs.Equal(t0.Add(3*time.Hour)) {
		t.Fatalf("expiry = %v %v", exp, abs)
	}
	if got := p.Extend(t0.Add(30*time.Minute), abs); !got.Equal(t0.Add(90 * time.Minute)) {
		t.Fatalf("Extend = %v", got)
	}
	if got := p.Extend(t0.Add(150*time.Minute), abs); !got.Equal(abs) {
		t.Fatalf("Extend must cap at absolute, got %v", got)
	}
	if p.NeedsTouch(t0, t0.Add(59*time.Second)) || !p.NeedsTouch(t0, t0.Add(time.Minute)) {
		t.Fatal("NeedsTouch mismatch")
	}

	c := p.Cookie("tok", exp, t0)
	if c.Name != SecureCookieName || c.Path != "/" || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.MaxAge != 3600 || c.Domain != "" {
		t.Fatalf("cookie = %+v", c)
	}
	if p.Cookie("tok", t0, t0).MaxAge != -1 || p.ClearCookie().MaxAge != -1 {
		t.Fatal("expired and cleared cookies must use MaxAge -1")
	}
	plain := SessionPolicy{}
	if plain.CookieName() != PlainCookieName || plain.Cookie("x", exp, t0).Secure {
		t.Fatal("insecure policy must not use __Host- or Secure")
	}
}

func TestReadToken(t *testing.T) {
	p := SessionPolicy{Secure: true}
	tok := NewSessionToken()
	if !crypto.Equal(tok.Hash, crypto.HashToken(tok.Value)) {
		t.Fatal("hash mismatch")
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: SecureCookieName, Value: tok.Value})
	if got, ok := p.ReadToken(req); !ok || got != tok.Value {
		t.Fatalf("ReadToken = %q %v", got, ok)
	}
	for _, bad := range []string{"", "short", tok.Value + "a", tok.Value[:42] + "!"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{Name: SecureCookieName, Value: bad})
		if _, ok := p.ReadToken(req); ok {
			t.Errorf("ReadToken accepted %q", bad)
		}
	}
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: PlainCookieName, Value: tok.Value})
	if _, ok := p.ReadToken(req); ok {
		t.Fatal("secure policy must ignore the plain cookie")
	}
}

func TestFresh(t *testing.T) {
	cases := map[time.Duration]bool{
		0:                 true,
		-9 * time.Minute:  true,
		-10 * time.Minute: false,
		30 * time.Second:  true,
		2 * time.Minute:   false,
	}
	for offset, want := range cases {
		if got := Fresh(t0.Add(offset), t0); got != want {
			t.Errorf("Fresh(%v) = %v, want %v", offset, got, want)
		}
	}
	if Fresh(time.Time{}, t0) {
		t.Fatal("zero reauth time must not be fresh")
	}
}
