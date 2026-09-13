package auth

import (
	"encoding/base64"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
)

func TestCSRFToken(t *testing.T) {
	secret := crypto.RandomBytes(32)
	a, b := CSRFToken(secret), CSRFToken(secret)
	if a == b {
		t.Fatal("tokens must be masked per render")
	}
	if !VerifyCSRF(secret, a) || !VerifyCSRF(secret, b) {
		t.Fatal("valid tokens rejected")
	}
	if VerifyCSRF(crypto.RandomBytes(32), a) || VerifyCSRF(secret, "") || VerifyCSRF(secret, a[:len(a)-2]) {
		t.Fatal("invalid tokens accepted")
	}
	raw, _ := base64.RawURLEncoding.DecodeString(a)
	raw[63] ^= 1
	if VerifyCSRF(secret, base64.RawURLEncoding.EncodeToString(raw)) {
		t.Fatal("tampered token accepted")
	}
}

func TestCheckOrigin(t *testing.T) {
	const origin = "https://calendar.akarpov.ru"
	cases := []struct {
		method, site, origin string
		ok                   bool
	}{
		{"GET", "cross-site", "https://evil.test", true},
		{"POST", "", "", true},
		{"POST", "same-origin", origin, true},
		{"POST", "none", "", true},
		{"POST", "", "HTTPS://CALENDAR.AKARPOV.RU", true},
		{"POST", "same-site", origin, false},
		{"POST", "cross-site", "", false},
		{"POST", "", "https://evil.test", false},
		{"DELETE", "", "null", false},
		{"PATCH", "same-origin", "https://calendar.akarpov.ru.evil.test", false},
		// A Referrer-Policy: no-referrer page, such as a public share, posts to
		// its own site with Origin: null. Only Sec-Fetch-Site can vouch for it.
		{"POST", "same-origin", "null", true},
		{"POST", "none", "null", false},
		{"POST", "cross-site", "null", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, "/", nil)
		if c.site != "" {
			req.Header.Set("Sec-Fetch-Site", c.site)
		}
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		err := CheckOrigin(req, origin)
		if c.ok != (err == nil) || (err != nil && !errors.Is(err, ErrCrossOrigin)) {
			t.Errorf("%s site=%q origin=%q err = %v", c.method, c.site, c.origin, err)
		}
	}
}
