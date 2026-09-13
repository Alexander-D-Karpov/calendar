package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/config"
	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
)

func TestSealedCookie(t *testing.T) {
	keys, err := crypto.NewKeyRing(map[uint32][]byte{1: bytes.Repeat([]byte{3}, crypto.KeySize)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: &config.Config{}, keys: keys}
	type payload struct {
		A string
		N int
	}
	rec := httptest.NewRecorder()
	if err := s.SetSealed(rec, "oauth", payload{"state", 3}, time.Minute); err != nil {
		t.Fatal(err)
	}
	c := rec.Result().Cookies()[0]
	if c.Name != "oauth" || !c.HttpOnly || c.MaxAge != 60 || c.Secure {
		t.Fatalf("cookie = %+v", c)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(c)
	var got payload
	if !s.ReadSealed(req, "oauth", &got) || got != (payload{"state", 3}) {
		t.Fatalf("read = %+v", got)
	}

	renamed := *c
	renamed.Name = "glink"
	other := httptest.NewRequest(http.MethodGet, "/", nil)
	other.AddCookie(&renamed)
	if s.ReadSealed(other, "glink", &got) {
		t.Fatal("a sealed value must be bound to its cookie name")
	}

	tampered := *c
	tampered.Value = c.Value[:len(c.Value)-2] + "AA"
	bad := httptest.NewRequest(http.MethodGet, "/", nil)
	bad.AddCookie(&tampered)
	if s.ReadSealed(bad, "oauth", &got) {
		t.Fatal("tampered cookie accepted")
	}

	out := httptest.NewRecorder()
	if !s.TakeSealed(out, req, "oauth", &got) || out.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("take must read and clear")
	}

	s.cfg.Security.CookieSecure = true
	rec = httptest.NewRecorder()
	_ = s.SetSealed(rec, "oauth", payload{}, time.Minute)
	if sc := rec.Result().Cookies()[0]; sc.Name != "__Host-oauth" || !sc.Secure {
		t.Fatalf("secure cookie = %+v", sc)
	}
}
