package web

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/httpx"
	"github.com/Alexander-D-Karpov/calendar/internal/ratelimit"
)

type errorView struct {
	Status int
	Title  string
	Detail string
}

func (s *Server) Fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrReauthRequired):
		s.Redirect(w, r, "/reauth?next="+url.QueryEscape(s.returnPath(r)))
	case errors.Is(err, domain.ErrUnauthorized) && auth.PrincipalFrom(r.Context()) == nil:
		s.Redirect(w, r, "/login?next="+url.QueryEscape(s.returnPath(r)))
	default:
		s.errorPage(w, r, err)
	}
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.errorPage(w, r, domain.ErrNotFound)
}

func StatusFor(err error) int {
	return httpx.StatusFor(err)
}

func (s *Server) errorPage(w http.ResponseWriter, r *http.Request, err error) {
	status := httpx.StatusFor(err)
	v := errorView{Status: status, Title: http.StatusText(status)}
	var rl *ratelimit.Error
	switch {
	case status >= 500:
		s.logger.LogAttrs(r.Context(), slog.LevelError, "request failed",
			slog.String("route", httpx.RouteLabel(r.Context())), slog.Any("err", err))
		v.Detail = "Something went wrong. The error has been logged."
	case errors.As(err, &rl):
		ratelimit.SetRetryAfter(w.Header(), rl.RetryAfter)
		v.Detail = "Too many requests. Try again in " + humanWait(rl.RetryAfter) + "."
	case errors.Is(err, auth.ErrCSRF), errors.Is(err, auth.ErrCrossOrigin):
		v.Detail = "The form expired or was sent from another site. Go back, reload the page and try again."
	case status == http.StatusNotFound:
		v.Detail = "This page does not exist or you do not have access to it."
	case status == http.StatusForbidden:
		v.Detail = "You do not have access to this page."
	case status == http.StatusRequestEntityTooLarge:
		v.Detail = "The request is too large."
	default:
		v.Detail = "The request could not be processed."
	}
	s.Render(w, r, status, "error", &Page{Title: v.Title, Data: v})
}

func (s *Server) FormError(w http.ResponseWriter, r *http.Request, err error, name string, p *Page) {
	if errors.Is(err, auth.ErrReauthRequired) || !p.Form.SetError(err) {
		s.Fail(w, r, err)
		return
	}
	var rl *ratelimit.Error
	if errors.As(err, &rl) {
		ratelimit.SetRetryAfter(w.Header(), rl.RetryAfter)
	}
	s.Render(w, r, httpx.StatusFor(err), name, p)
}

func (s *Server) Retry(w http.ResponseWriter, r *http.Request, err error, f *Form, render func(status int)) {
	switch {
	case errors.Is(err, domain.ErrPrecondition):
		f.Error = "This item was changed elsewhere. Reload it and try again."
	case !f.SetError(err):
		s.Fail(w, r, err)
		return
	}
	render(httpx.StatusFor(err))
}

// Redirect re-checks the target with SafeNext rather than trusting the caller.
// Every site already passes a path it built itself, and re-checking keeps a
// future one from turning this helper into an open redirect; a safe path is
// returned unchanged, so no current caller is affected.
func (s *Server) Redirect(w http.ResponseWriter, r *http.Request, target string) {
	http.Redirect(w, r, SafeNext(target), http.StatusSeeOther)
}

func (s *Server) BackTo(r *http.Request, back string) string {
	if back != "" {
		return SafeNext(back)
	}
	return s.refererPath(r)
}

func (s *Server) returnPath(r *http.Request) string {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return SafeNext(r.URL.RequestURI())
	}
	return s.refererPath(r)
}

func (s *Server) refererPath(r *http.Request) string {
	u, err := url.Parse(r.Referer())
	if err != nil || u.Scheme+"://"+u.Host != s.cfg.App.Origin() {
		return "/"
	}
	return SafeNext(u.RequestURI())
}
