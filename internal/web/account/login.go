package account

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

type registerView struct {
	MinPassword int
}

type reauthView struct {
	Password bool
	Google   bool
}

func (a *Pages) loginPage(w http.ResponseWriter, r *http.Request) {
	next := web.SafeNext(r.URL.Query().Get("next"))
	if auth.PrincipalFrom(r.Context()) != nil {
		a.site.Redirect(w, r, next)
		return
	}
	a.site.Render(w, r, http.StatusOK, "login", &web.Page{Title: "Sign in", Form: web.NewForm(url.Values{"next": {next}})})
}

func (a *Pages) login(w http.ResponseWriter, r *http.Request) {
	f, ok := a.site.ParseForm(w, r)
	if !ok {
		return
	}
	iss, err := a.auth.Login(r.Context(), f.Get("email"), r.PostForm.Get("password"), auth.MetaFrom(r))
	if err != nil {
		a.site.FormError(w, r, err, "login", &web.Page{Title: "Sign in", Form: f})
		return
	}
	a.startSession(w, r, iss)
	a.site.Redirect(w, r, web.SafeNext(f.Get("next")))
}

func (a *Pages) registerPage(w http.ResponseWriter, r *http.Request) {
	if !a.site.RegistrationOpen() {
		a.site.Fail(w, r, domain.ErrNotFound)
		return
	}
	if auth.PrincipalFrom(r.Context()) != nil {
		a.site.Redirect(w, r, "/")
		return
	}
	a.site.Render(w, r, http.StatusOK, "register", a.registerData(nil))
}

func (a *Pages) register(w http.ResponseWriter, r *http.Request) {
	if !a.site.RegistrationOpen() {
		a.site.Fail(w, r, domain.ErrNotFound)
		return
	}
	f, ok := a.site.ParseForm(w, r)
	if !ok {
		return
	}
	pw := r.PostForm.Get("password")
	if pw != r.PostForm.Get("password_confirm") {
		f.Errors["password_confirm"] = "Passwords do not match."
		a.site.Render(w, r, http.StatusUnprocessableEntity, "register", a.registerData(f))
		return
	}
	meta := auth.MetaFrom(r)
	u, err := a.auth.Register(r.Context(), auth.RegisterInput{
		Email:       f.Get("email"),
		Password:    pw,
		DisplayName: f.Get("display_name"),
		Timezone:    f.Get("timezone"),
	}, meta)
	if err != nil {
		a.site.FormError(w, r, err, "register", a.registerData(f))
		return
	}
	if a.sendVerify(w, r, u) {
		a.site.Flash(w, r, "ok", "Account created. Confirm your email to sign in.")
		a.site.Redirect(w, r, "/login")
		return
	}
	iss, err := a.auth.StartSession(r.Context(), u, meta)
	if err != nil {
		a.site.Fail(w, r, err)
		return
	}
	a.guard.SetSessionCookie(w, iss)
	a.site.Flash(w, r, "ok", "Your account is ready.")
	a.site.Redirect(w, r, "/")
}

func (a *Pages) registerData(f *web.Form) *web.Page {
	if f == nil {
		f = web.NewForm(nil)
	}
	return &web.Page{Title: "Create account", Form: f, Data: registerView{MinPassword: a.cfg.Security.PasswordMinLength}}
}

func (a *Pages) logout(w http.ResponseWriter, r *http.Request) {
	if err := a.auth.Logout(r.Context(), auth.PrincipalFrom(r.Context()), auth.MetaFrom(r)); err != nil {
		a.site.Fail(w, r, err)
		return
	}
	a.guard.ClearSessionCookie(w)
	a.site.Redirect(w, r, "/login")
}

func (a *Pages) startSession(w http.ResponseWriter, r *http.Request, iss *auth.Issued) {
	if prev := auth.PrincipalFrom(r.Context()); prev.IsSession() {
		_ = a.auth.Logout(r.Context(), prev, auth.MetaFrom(r))
	}
	a.guard.SetSessionCookie(w, iss)
}

func (a *Pages) reauthPage(w http.ResponseWriter, r *http.Request) {
	a.renderReauth(w, r, http.StatusOK, web.NewForm(url.Values{"next": {web.SafeNext(r.URL.Query().Get("next"))}}))
}

func (a *Pages) renderReauth(w http.ResponseWriter, r *http.Request, status int, f *web.Form) {
	p := auth.PrincipalFrom(r.Context())
	u, err := a.auth.User(r.Context(), p.UserID)
	if err != nil {
		a.site.Fail(w, r, err)
		return
	}
	ids, err := a.auth.Identities(r.Context(), p.UserID)
	if err != nil {
		a.site.Fail(w, r, err)
		return
	}
	rv := reauthView{Password: u.HasPassword(), Google: a.google != nil && googleIdentity(ids) != nil}
	a.site.Render(w, r, status, "reauth", &web.Page{Title: "Confirm it is you", User: &u, Form: f, Data: rv})
}

func (a *Pages) reauth(w http.ResponseWriter, r *http.Request) {
	f, ok := a.site.ParseForm(w, r)
	if !ok {
		return
	}
	err := a.auth.Reauth(r.Context(), auth.PrincipalFrom(r.Context()), r.PostForm.Get("password"), auth.MetaFrom(r))
	if err != nil {
		if errors.Is(err, auth.ErrReauthRequired) {
			a.site.Fail(w, r, err)
			return
		}
		a.site.Retry(w, r, err, f, func(status int) { a.renderReauth(w, r, status, f) })
		return
	}
	a.site.Redirect(w, r, web.SafeNext(f.Get("next")))
}
