package auth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/httpx"
)

const testOrigin = "https://calendar.test"

type probe struct {
	p *Principal
}

func (pr *probe) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pr.p = PrincipalFrom(r.Context())
	})
}

type reqOpt func(*http.Request)

func withCookie(v string) reqOpt {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: SecureCookieName, Value: v}) }
}

func withHeader(k, v string) reqOpt {
	return func(r *http.Request) { r.Header.Set(k, v) }
}

func failStatus(w http.ResponseWriter, _ *http.Request, err error) {
	http.Error(w, err.Error(), httpx.StatusFor(err))
}

func send(h http.Handler, method, body string, opts ...reqOpt) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, "/x", rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, o := range opts {
		o(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func guardFixture(t *testing.T) (*Guard, *Service, *Issued, *clock.Fake) {
	t.Helper()
	s, _, clk := newService(t, nil)
	register(t, s, "alice@example.com")
	iss, err := s.Login(context.Background(), "alice@example.com", testPassword, meta)
	if err != nil {
		t.Fatal(err)
	}
	return NewGuard(s, testOrigin, failStatus), s, iss, clk
}

func TestGuardWeb(t *testing.T) {
	g, _, iss, clk := guardFixture(t)
	pr := &probe{}
	h := httpx.Chain(pr.handler(), g.Session, g.CSRF, g.RequireUser)
	cookie := withCookie(iss.Token)
	csrf := CSRFToken(iss.Session.CSRFSecret)

	if rec := send(h, "GET", "", cookie); rec.Code != http.StatusOK || pr.p == nil || pr.p.UserID != iss.User.ID {
		t.Fatalf("GET = %d %+v", rec.Code, pr.p)
	}
	if rec := send(h, "POST", "", cookie); rec.Code != http.StatusForbidden {
		t.Fatalf("POST without csrf = %d", rec.Code)
	}
	if rec := send(h, "POST", "", cookie, withHeader(CSRFHeader, csrf)); rec.Code != http.StatusOK {
		t.Fatalf("POST with header = %d", rec.Code)
	}
	if rec := send(h, "POST", CSRFField+"="+csrf, cookie); rec.Code != http.StatusOK {
		t.Fatalf("POST with form field = %d", rec.Code)
	}
	if rec := send(h, "POST", "", cookie, withHeader(CSRFHeader, csrf), withHeader("Sec-Fetch-Site", "cross-site")); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site POST = %d", rec.Code)
	}

	pr.p = nil
	if rec := send(h, "GET", ""); rec.Code != http.StatusUnauthorized || pr.p != nil {
		t.Fatalf("anonymous GET = %d", rec.Code)
	}
	rec := send(h, "GET", "", withCookie(strings.Repeat("A", sessionTokenLen)))
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("stale cookie = %d %q", rec.Code, rec.Header().Get("Set-Cookie"))
	}

	clk.Advance(2 * time.Minute)
	rec = send(h, "GET", "", cookie)
	c := rec.Header().Get("Set-Cookie")
	if rec.Code != http.StatusOK || !strings.HasPrefix(c, SecureCookieName+"="+iss.Token) || !strings.Contains(c, "Max-Age=3600") {
		t.Fatalf("refresh = %d %q", rec.Code, c)
	}
}

func TestGuardAPI(t *testing.T) {
	g, s, iss, _ := guardFixture(t)
	p, err := s.Authenticate(context.Background(), iss.Token)
	if err != nil {
		t.Fatal(err)
	}
	raw, tok, err := s.CreateAPIToken(context.Background(), p, TokenInput{Name: "t", Scopes: []string{"todos:read"}}, meta)
	if err != nil {
		t.Fatal(err)
	}
	pr := &probe{}
	read := httpx.Chain(pr.handler(), g.Session, g.API, g.RequireScope(ScopeTodosRead))
	write := httpx.Chain(pr.handler(), g.Session, g.API, g.RequireScope(ScopeTodosWrite))
	bearer := withHeader("Authorization", "Bearer "+raw)
	cookie := withCookie(iss.Token)

	if rec := send(read, "GET", "", bearer); rec.Code != http.StatusOK || pr.p.TokenID != tok.ID {
		t.Fatalf("bearer GET = %d", rec.Code)
	}
	if rec := send(read, "POST", "", bearer); rec.Code != http.StatusOK {
		t.Fatalf("bearer POST must not need csrf, got %d", rec.Code)
	}
	if rec := send(write, "GET", "", bearer); rec.Code != http.StatusForbidden {
		t.Fatalf("missing scope = %d", rec.Code)
	}
	if rec := send(read, "GET", "", withHeader("Authorization", "Bearer cal_nope")); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad bearer = %d", rec.Code)
	}
	if rec := send(write, "GET", "", cookie); rec.Code != http.StatusOK || !pr.p.IsSession() {
		t.Fatalf("session GET = %d", rec.Code)
	}
	if rec := send(write, "POST", "", cookie); rec.Code != http.StatusForbidden {
		t.Fatalf("session POST without csrf = %d", rec.Code)
	}
	if rec := send(write, "POST", "", cookie, withHeader(CSRFHeader, CSRFToken(iss.Session.CSRFSecret))); rec.Code != http.StatusOK {
		t.Fatalf("session POST with csrf = %d", rec.Code)
	}
	if rec := send(read, "GET", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", rec.Code)
	}
}

func TestGuardRequireFresh(t *testing.T) {
	g, _, iss, clk := guardFixture(t)
	h := httpx.Chain((&probe{}).handler(), g.Session, g.RequireFresh)
	if rec := send(h, "GET", "", withCookie(iss.Token)); rec.Code != http.StatusOK {
		t.Fatalf("fresh = %d", rec.Code)
	}
	clk.Advance(11 * time.Minute)
	if rec := send(h, "GET", "", withCookie(iss.Token)); rec.Code != http.StatusForbidden {
		t.Fatalf("stale = %d", rec.Code)
	}
}
