package account

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/httpx"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

type expiryOption struct {
	Value string
	Label string
}

var tokenExpiry = []expiryOption{
	{"30", "30 days"},
	{"90", "90 days"},
	{"365", "1 year"},
	{"", "Never"},
}

type tokensView struct {
	Tokens []domain.APIToken
	Scopes []string
	Expiry []expiryOption
	Now    time.Time
	Fresh  bool
}

type tokenCreatedView struct {
	Token  string
	Info   domain.APIToken
	Origin string
}

func (a *Pages) tokensPage(w http.ResponseWriter, r *http.Request) {
	a.renderTokens(w, r, http.StatusOK, web.NewForm(url.Values{"expires": {"90"}}))
}

func (a *Pages) renderTokens(w http.ResponseWriter, r *http.Request, status int, f *web.Form) {
	p := auth.PrincipalFrom(r.Context())
	list, err := a.auth.ListAPITokens(r.Context(), p.UserID)
	if err != nil {
		a.site.Fail(w, r, err)
		return
	}
	now := a.clock.Now()
	a.site.Render(w, r, status, "settings/tokens", &web.Page{
		Title: "API tokens",
		Nav:   "settings/tokens",
		Form:  f,
		Data: tokensView{
			Tokens: list,
			Scopes: auth.ScopeStrings(auth.AllScopes),
			Expiry: tokenExpiry,
			Now:    now,
			Fresh:  p.Fresh(now),
		},
	})
}

func (a *Pages) createToken(w http.ResponseWriter, r *http.Request) {
	f, ok := a.site.ParseForm(w, r)
	if !ok {
		return
	}
	exp, valid := expiryFrom(f.Get("expires"), a.clock.Now())
	if !valid {
		f.Errors["expires_at"] = "Choose one of the listed options."
		a.renderTokens(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	raw, tok, err := a.auth.CreateAPIToken(r.Context(), auth.PrincipalFrom(r.Context()), auth.TokenInput{
		Name:      f.Get("name"),
		Scopes:    f.Values["scopes"],
		ExpiresAt: exp,
	}, auth.MetaFrom(r))
	if err != nil {
		if errors.Is(err, auth.ErrReauthRequired) || !f.SetError(err) {
			a.site.Fail(w, r, err)
			return
		}
		a.renderTokens(w, r, httpx.StatusFor(err), f)
		return
	}
	a.site.Render(w, r, http.StatusOK, "settings/token_created", &web.Page{
		Title: "Token created",
		Nav:   "settings/tokens",
		Data:  tokenCreatedView{Token: raw, Info: tok, Origin: a.cfg.App.Origin()},
	})
}

func (a *Pages) revokeToken(w http.ResponseWriter, r *http.Request) {
	id, err := domain.ParseID(r.PathValue("id"))
	if err != nil {
		a.site.Fail(w, r, domain.ErrNotFound)
		return
	}
	if err := a.auth.RevokeAPIToken(r.Context(), auth.PrincipalFrom(r.Context()).UserID, id, auth.MetaFrom(r)); err != nil {
		a.site.Fail(w, r, err)
		return
	}
	a.site.Flash(w, r, "ok", "The token was revoked.")
	a.site.Redirect(w, r, "/settings/tokens")
}

func expiryFrom(v string, now time.Time) (*time.Time, bool) {
	if v == "" {
		return nil, true
	}
	for _, o := range tokenExpiry {
		if o.Value != v {
			continue
		}
		days, err := strconv.Atoi(v)
		if err != nil {
			return nil, false
		}
		t := now.AddDate(0, 0, days)
		return &t, true
	}
	return nil, false
}
