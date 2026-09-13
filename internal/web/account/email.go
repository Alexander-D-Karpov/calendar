package account

import (
	"net/http"
	"net/url"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/mail"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

type verifyView struct {
	OK      bool
	Message string
	Resend  bool
}

// sendVerify reports whether a link went out, which is also the signal that the
// account must confirm before it can sign in.
func (a *Pages) sendVerify(w http.ResponseWriter, r *http.Request, u domain.User) bool {
	tok, sent, err := a.auth.IssueVerify(r.Context(), u)
	if err != nil || !sent {
		return false
	}
	a.site.SendMail(r.Context(), mail.KindVerify, u.Email, mail.SubjectVerify, mail.Verify(mail.Data{
		AppName: a.cfg.App.Name,
		Name:    u.Name(),
		URL:     a.cfg.App.URL("/verify?token=" + url.QueryEscape(tok)),
	}))
	return true
}

func (a *Pages) verifyPage(w http.ResponseWriter, r *http.Request) {
	signedIn := auth.PrincipalFrom(r.Context()).IsSession()
	_, err := a.auth.ConfirmVerify(r.Context(), r.URL.Query().Get("token"), auth.MetaFrom(r))
	if err == nil {
		a.site.Flash(w, r, "ok", "Email confirmed.")
		target := "/login"
		if signedIn {
			target = "/"
		}
		a.site.Redirect(w, r, target)
		return
	}
	msg := web.ErrorText(err)
	if msg == "" {
		a.site.Fail(w, r, err)
		return
	}
	a.site.Render(w, r, http.StatusBadRequest, "verify", &web.Page{
		Title: "Confirm your email",
		Data:  verifyView{Message: msg, Resend: signedIn},
	})
}

func (a *Pages) resendVerify(w http.ResponseWriter, r *http.Request) {
	u, err := a.auth.User(r.Context(), auth.PrincipalFrom(r.Context()).UserID)
	if err != nil {
		a.site.Fail(w, r, err)
		return
	}
	if a.sendVerify(w, r, u) {
		a.site.Flash(w, r, "ok", "Check your inbox.")
	} else {
		a.site.Flash(w, r, "info", "That address is already confirmed.")
	}
	a.site.Redirect(w, r, a.site.BackTo(r, securityPath))
}

func (a *Pages) forgotPage(w http.ResponseWriter, r *http.Request) {
	if !a.cfg.SMTP.Enabled() {
		a.site.Fail(w, r, domain.ErrNotFound)
		return
	}
	a.site.Render(w, r, http.StatusOK, "forgot", &web.Page{Title: "Reset your password"})
}

func (a *Pages) forgot(w http.ResponseWriter, r *http.Request) {
	if !a.cfg.SMTP.Enabled() {
		a.site.Fail(w, r, domain.ErrNotFound)
		return
	}
	f, ok := a.site.ParseForm(w, r)
	if !ok {
		return
	}
	u, tok, sent, err := a.auth.IssueReset(r.Context(), f.Get("email"), auth.MetaFrom(r))
	if err != nil {
		a.site.Retry(w, r, err, f, func(status int) {
			a.site.Render(w, r, status, "forgot", &web.Page{Title: "Reset your password", Form: f})
		})
		return
	}
	if sent {
		a.site.SendMail(r.Context(), mail.KindReset, u.Email, mail.SubjectReset, mail.Reset(mail.Data{
			AppName: a.cfg.App.Name,
			Name:    u.Name(),
			URL:     a.cfg.App.URL("/reset?token=" + url.QueryEscape(tok)),
		}))
	}
	a.site.Flash(w, r, "ok", "If that address has an account, a reset link is on its way.")
	a.site.Redirect(w, r, "/login")
}

// resetPage does not touch the token: a link preview that fetches the page must
// not burn it. It is checked when the new password arrives.
func (a *Pages) resetPage(w http.ResponseWriter, r *http.Request) {
	if !a.cfg.SMTP.Enabled() {
		a.site.Fail(w, r, domain.ErrNotFound)
		return
	}
	f := web.NewForm(url.Values{"token": {r.URL.Query().Get("token")}})
	a.site.Render(w, r, http.StatusOK, "reset", &web.Page{Title: "Choose a new password", Form: f})
}

func (a *Pages) reset(w http.ResponseWriter, r *http.Request) {
	if !a.cfg.SMTP.Enabled() {
		a.site.Fail(w, r, domain.ErrNotFound)
		return
	}
	f, ok := a.site.ParseForm(w, r)
	if !ok {
		return
	}
	render := func(status int) {
		a.site.Render(w, r, status, "reset", &web.Page{Title: "Choose a new password", Form: f})
	}
	pw := r.PostForm.Get("password")
	if pw != r.PostForm.Get("password_confirm") {
		f.Errors["password_confirm"] = "Passwords do not match."
		render(http.StatusUnprocessableEntity)
		return
	}
	if err := a.auth.ResetPassword(r.Context(), f.Get("token"), pw, auth.MetaFrom(r)); err != nil {
		a.site.Retry(w, r, err, f, render)
		return
	}
	a.site.Flash(w, r, "ok", "Password changed. Sign in with it.")
	a.site.Redirect(w, r, "/login")
}
