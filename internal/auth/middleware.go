package auth

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/httpx"
	"github.com/Alexander-D-Karpov/calendar/internal/ratelimit"
)

type Fail func(w http.ResponseWriter, r *http.Request, err error)

type Guard struct {
	svc      *Service
	policy   SessionPolicy
	origin   string
	clock    clock.Clock
	fail     Fail
	failures *ratelimit.Limiter
}

func NewGuard(svc *Service, origin string, fail Fail) *Guard {
	return &Guard{svc: svc, policy: svc.policy, origin: origin, clock: svc.clock, fail: fail}
}

func (g *Guard) WithFail(fail Fail) *Guard {
	c := *g
	c.fail = fail
	return &c
}

func (g *Guard) WithAuthFailures(l *ratelimit.Limiter) *Guard {
	c := *g
	c.failures = l
	return &c
}

func MetaFrom(r *http.Request) Meta {
	return Meta{IP: httpx.ClientIPFrom(r.Context()), UserAgent: r.UserAgent()}
}

func ByPrincipal(r *http.Request) string {
	if p := PrincipalFrom(r.Context()); p != nil {
		return "user:" + p.UserID.String()
	}
	return ratelimit.ByIP(r)
}

func (g *Guard) SetSessionCookie(w http.ResponseWriter, iss *Issued) {
	http.SetCookie(w, g.policy.Cookie(iss.Token, iss.Session.ExpiresAt, g.clock.Now()))
}

func (g *Guard) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, g.policy.ClearCookie())
}

func (g *Guard) Session(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, ok := g.policy.ReadToken(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		p, err := g.svc.Authenticate(r.Context(), tok)
		switch {
		case errors.Is(err, domain.ErrUnauthorized):
			g.ClearSessionCookie(w)
			next.ServeHTTP(w, r)
			return
		case err != nil:
			g.fail(w, r, err)
			return
		}
		if p.Refreshed {
			http.SetCookie(w, g.policy.Cookie(tok, p.ExpiresAt, g.clock.Now()))
		}
		next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
	})
}

func (g *Guard) RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if PrincipalFrom(r.Context()) == nil {
			g.fail(w, r, domain.ErrUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (g *Guard) CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if IsSafeMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		if err := CheckOrigin(r, g.origin); err != nil {
			g.fail(w, r, err)
			return
		}
		p := PrincipalFrom(r.Context())
		if !p.IsSession() {
			if r.Header.Get("Origin") == "" && r.Header.Get("Sec-Fetch-Site") == "" {
				g.fail(w, r, ErrCrossOrigin)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		tok := r.Header.Get(CSRFHeader)
		if tok == "" {
			tok = r.PostFormValue(CSRFField)
		}
		if !VerifyCSRF(p.CSRFSecret, tok) {
			g.fail(w, r, ErrCSRF)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (g *Guard) API(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if raw, ok := BearerToken(r); ok {
			g.bearer(w, r, next, raw)
			return
		}
		p := PrincipalFrom(r.Context())
		if p == nil {
			g.fail(w, r, domain.ErrUnauthorized)
			return
		}
		if !IsSafeMethod(r.Method) {
			if err := CheckOrigin(r, g.origin); err != nil {
				g.fail(w, r, err)
				return
			}
			if !VerifyCSRF(p.CSRFSecret, r.Header.Get(CSRFHeader)) {
				g.fail(w, r, ErrCSRF)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (g *Guard) bearer(w http.ResponseWriter, r *http.Request, next http.Handler, raw string) {
	key := ratelimit.ByIP(r)
	track := g.failures != nil && key != ""
	if track {
		if wait, blocked := g.failures.Blocked(key); blocked {
			g.fail(w, r, &ratelimit.Error{RetryAfter: wait})
			return
		}
	}
	p, err := g.svc.AuthenticateAPIToken(r.Context(), raw, MetaFrom(r))
	if err != nil {
		if track && errors.Is(err, domain.ErrUnauthorized) {
			g.failures.Allow(key)
		}
		g.fail(w, r, err)
		return
	}
	next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
}

func (g *Guard) RequireScope(s Scope) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := PrincipalFrom(r.Context())
			switch {
			case p == nil:
				g.fail(w, r, domain.ErrUnauthorized)
			case !p.Can(s):
				g.fail(w, r, fmt.Errorf("%w: token lacks scope %s", domain.ErrForbidden, s))
			default:
				next.ServeHTTP(w, r)
			}
		})
	}
}

func (g *Guard) RequireFresh(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := PrincipalFrom(r.Context())
		switch {
		case p == nil:
			g.fail(w, r, domain.ErrUnauthorized)
		case !p.Fresh(g.clock.Now()):
			g.fail(w, r, ErrReauthRequired)
		default:
			next.ServeHTTP(w, r)
		}
	})
}
