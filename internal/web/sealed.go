package web

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
)

const sealedMaxBytes = 3000

func (s *Server) cookieName(name string) string {
	if s.cfg.Security.CookieSecure {
		return "__Host-" + name
	}
	return name
}

func (s *Server) SetSealed(w http.ResponseWriter, name string, v any, ttl time.Duration) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	val := base64.RawURLEncoding.EncodeToString(s.keys.Seal(b, crypto.AAD("cookie", name)))
	if len(val) > sealedMaxBytes {
		return errors.New("web: sealed cookie too large")
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName(name),
		Value:    val,
		Path:     "/",
		MaxAge:   int(ttl / time.Second),
		HttpOnly: true,
		Secure:   s.cfg.Security.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (s *Server) ReadSealed(r *http.Request, name string, v any) bool {
	c, err := r.Cookie(s.cookieName(name))
	if err != nil {
		return false
	}
	ct, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return false
	}
	pt, err := s.keys.Open(ct, crypto.AAD("cookie", name))
	if err != nil {
		return false
	}
	return json.Unmarshal(pt, v) == nil
}

func (s *Server) TakeSealed(w http.ResponseWriter, r *http.Request, name string, v any) bool {
	ok := s.ReadSealed(r, name, v)
	s.ClearCookie(w, name)
	return ok
}

func (s *Server) ClearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName(name),
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cfg.Security.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}
