package account

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/google"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

const (
	syncPath    = "/settings/google"
	oauthCookie = "oauth"
	oauthTTL    = 10 * time.Minute

	intentLogin  = "login"
	intentLink   = "link"
	intentReauth = "reauth"
	intentGrant  = "grant"
)

type oauthState struct {
	State    string    `json:"s"`
	Nonce    string    `json:"n"`
	Verifier string    `json:"v"`
	Intent   string    `json:"i"`
	Next     string    `json:"x"`
	Timezone string    `json:"z,omitempty"`
	Session  domain.ID `json:"u"`
	Expires  int64     `json:"e"`
}

func (a *Pages) googleStart(w http.ResponseWriter, r *http.Request) {
	if a.google == nil {
		a.site.Fail(w, r, domain.ErrNotFound)
		return
	}
	f, ok := a.site.ParseForm(w, r)
	if !ok {
		return
	}
	now := a.clock.Now()
	st := oauthState{
		State:    crypto.RandomToken(32),
		Nonce:    crypto.RandomToken(32),
		Verifier: google.NewVerifier(),
		Intent:   f.Get("intent"),
		Next:     web.SafeNext(f.Get("next")),
		Timezone: f.Get("timezone"),
		Expires:  now.Add(oauthTTL).Unix(),
	}
	opts := google.AuthOptions{SelectAccount: true}
	p := auth.PrincipalFrom(r.Context())
	switch st.Intent {
	case intentLogin:
	case intentLink, intentReauth, intentGrant:
		if !p.IsSession() {
			a.site.Fail(w, r, domain.ErrUnauthorized)
			return
		}
		if st.Intent == intentGrant && a.sync == nil {
			a.site.Fail(w, r, domain.ErrNotFound)
			return
		}
		st.Session = p.SessionID
		if st.Intent != intentLink {
			ids, err := a.auth.Identities(r.Context(), p.UserID)
			if err != nil {
				a.site.Fail(w, r, err)
				return
			}
			if g := googleIdentity(ids); g != nil {
				opts.LoginHint = g.Email
			}
		}
		if st.Intent == intentGrant {
			opts.Scopes, opts.Offline = google.SyncScopes, true
		}
	default:
		a.site.Fail(w, r, fmt.Errorf("%w: unknown sign-in intent", domain.ErrInvalid))
		return
	}
	if err := a.site.SetSealed(w, oauthCookie, st, oauthTTL); err != nil {
		a.site.Fail(w, r, err)
		return
	}
	opts.State, opts.Nonce, opts.Verifier = st.State, st.Nonce, st.Verifier
	http.Redirect(w, r, a.google.AuthURL(opts), http.StatusSeeOther)
}

func (a *Pages) googleCallback(w http.ResponseWriter, r *http.Request) {
	if a.google == nil {
		a.site.Fail(w, r, domain.ErrNotFound)
		return
	}
	q := r.URL.Query()
	var st oauthState
	valid := a.site.TakeSealed(w, r, oauthCookie, &st) &&
		a.clock.Now().Unix() <= st.Expires &&
		crypto.Equal([]byte(st.State), []byte(q.Get("state")))
	if !valid {
		a.back(w, r, st, "error", "The Google sign-in expired or was started in another tab. Try again.")
		return
	}
	if q.Get("error") != "" {
		a.back(w, r, st, "info", "Google sign-in was cancelled.")
		return
	}
	c, tok, err := a.google.Exchange(r.Context(), q.Get("code"), st.Verifier, st.Nonce)
	if err != nil {
		a.site.Logger().LogAttrs(r.Context(), slog.LevelWarn, "google sign-in failed", slog.Any("err", err))
		a.back(w, r, st, "error", "Google sign-in failed. Try again.")
		return
	}
	x := auth.External{
		Provider:      domain.ProviderGoogle,
		Subject:       c.Subject,
		Email:         c.Email,
		EmailVerified: c.EmailVerified,
		Name:          c.Name,
		Timezone:      st.Timezone,
	}
	if st.Intent == intentLogin {
		a.googleLogin(w, r, st, x)
		return
	}
	p := auth.PrincipalFrom(r.Context())
	if !p.IsSession() || p.SessionID != st.Session {
		a.back(w, r, oauthState{Intent: intentLogin, Next: st.Next}, "error", "Your session changed during the Google sign-in. Try again.")
		return
	}
	meta := auth.MetaFrom(r)
	switch st.Intent {
	case intentReauth:
		if err := a.auth.ExternalReauth(r.Context(), p, x, meta); err != nil {
			a.refuse(w, r, st, err)
			return
		}
		a.site.Redirect(w, r, st.Next)
		return
	case intentGrant:
		a.grant(w, r, st, x, tok)
		return
	}
	if err := a.auth.LinkIdentity(r.Context(), p.UserID, x, meta); err != nil {
		a.refuse(w, r, st, err)
		return
	}
	a.site.Flash(w, r, "ok", "Google account linked.")
	a.site.Redirect(w, r, securityPath)
}

func (a *Pages) grant(w http.ResponseWriter, r *http.Request, st oauthState, x auth.External, tok *oauth2.Token) {
	ctx := r.Context()
	p := auth.PrincipalFrom(ctx)
	if err := a.auth.LinkIdentity(ctx, p.UserID, x, auth.MetaFrom(r)); err != nil {
		if errors.Is(err, auth.ErrAlreadyLinked) {
			err = auth.ErrWrongIdentity
		}
		a.refuse(w, r, st, err)
		return
	}
	ids, err := a.auth.Identities(ctx, p.UserID)
	if err != nil {
		a.site.Fail(w, r, err)
		return
	}
	gid := googleIdentity(ids)
	if gid == nil || gid.Subject != x.Subject {
		a.refuse(w, r, st, auth.ErrWrongIdentity)
		return
	}
	raw, _ := tok.Extra("scope").(string)
	scopes := strings.Fields(raw)
	if tok.RefreshToken == "" || !google.HasSyncScopes(scopes) {
		a.back(w, r, st, "error", "Google did not grant access to both Calendar and Tasks. Allow both and try again.")
		return
	}
	if err := a.sync.Connect(ctx, p.UserID, *gid, tok.RefreshToken, scopes); err != nil {
		a.site.Fail(w, r, err)
		return
	}
	a.site.Flash(w, r, "ok", "Google connected. Choose what to sync.")
	a.site.Redirect(w, r, syncPath)
}

func (a *Pages) googleLogin(w http.ResponseWriter, r *http.Request, st oauthState, x auth.External) {
	iss, err := a.auth.ExternalLogin(r.Context(), x, auth.MetaFrom(r))
	if err != nil {
		a.refuse(w, r, st, err)
		return
	}
	a.startSession(w, r, iss)
	a.site.Redirect(w, r, st.Next)
}

func (a *Pages) refuse(w http.ResponseWriter, r *http.Request, st oauthState, err error) {
	msg := web.ErrorText(err)
	if msg == "" {
		a.site.Fail(w, r, err)
		return
	}
	a.back(w, r, st, "error", msg)
}

func (a *Pages) back(w http.ResponseWriter, r *http.Request, st oauthState, kind, msg string) {
	a.site.Flash(w, r, kind, msg)
	next := url.QueryEscape(web.SafeNext(st.Next))
	switch st.Intent {
	case intentLink:
		a.site.Redirect(w, r, securityPath)
	case intentGrant:
		a.site.Redirect(w, r, syncPath)
	case intentReauth:
		a.site.Redirect(w, r, "/reauth?next="+next)
	default:
		a.site.Redirect(w, r, "/login?next="+next)
	}
}

func googleIdentity(ids []domain.Identity) *domain.Identity {
	for i := range ids {
		if ids[i].Provider == domain.ProviderGoogle {
			return &ids[i]
		}
	}
	return nil
}
