package auth

import (
	"net/http"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/config"
	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
)

const (
	SecureCookieName = "__Host-sid"
	PlainCookieName  = "sid"
	TouchInterval    = time.Minute
	SudoWindow       = 10 * time.Minute
	sessionTokenLen  = 43
)

type SessionPolicy struct {
	TTL         time.Duration
	AbsoluteTTL time.Duration
	Secure      bool
}

func NewSessionPolicy(s config.Security) SessionPolicy {
	return SessionPolicy{TTL: s.SessionTTL, AbsoluteTTL: s.SessionAbsoluteTTL, Secure: s.CookieSecure}
}

type SessionToken struct {
	Value string
	Hash  []byte
}

func NewSessionToken() SessionToken {
	v := crypto.RandomToken(32)
	return SessionToken{Value: v, Hash: crypto.HashToken(v)}
}

func (p SessionPolicy) CookieName() string {
	if p.Secure {
		return SecureCookieName
	}
	return PlainCookieName
}

func (p SessionPolicy) NewExpiry(now time.Time) (expires, absolute time.Time) {
	absolute = now.Add(p.AbsoluteTTL)
	return p.Extend(now, absolute), absolute
}

func (p SessionPolicy) Extend(now, absolute time.Time) time.Time {
	if e := now.Add(p.TTL); e.Before(absolute) {
		return e
	}
	return absolute
}

func (p SessionPolicy) NeedsTouch(lastSeen, now time.Time) bool {
	return now.Sub(lastSeen) >= TouchInterval
}

func (p SessionPolicy) Cookie(value string, expires, now time.Time) *http.Cookie {
	maxAge := int(expires.Sub(now) / time.Second)
	if maxAge <= 0 {
		maxAge = -1
	}
	return &http.Cookie{
		Name:     p.CookieName(),
		Value:    value,
		Path:     "/",
		Expires:  expires,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   p.Secure,
		SameSite: http.SameSiteLaxMode,
	}
}

func (p SessionPolicy) ClearCookie() *http.Cookie {
	return &http.Cookie{
		Name:     p.CookieName(),
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   p.Secure,
		SameSite: http.SameSiteLaxMode,
	}
}

func (p SessionPolicy) ReadToken(r *http.Request) (string, bool) {
	c, err := r.Cookie(p.CookieName())
	if err != nil || len(c.Value) != sessionTokenLen {
		return "", false
	}
	for i := 0; i < len(c.Value); i++ {
		ch := c.Value[i]
		if !(ch >= '0' && ch <= '9' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || ch == '-' || ch == '_') {
			return "", false
		}
	}
	return c.Value, true
}

func Fresh(reauthAt, now time.Time) bool {
	return !reauthAt.IsZero() && !reauthAt.After(now.Add(time.Minute)) && now.Sub(reauthAt) < SudoWindow
}
