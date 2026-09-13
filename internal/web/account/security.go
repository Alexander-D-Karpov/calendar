package account

import (
	"errors"
	"net/http"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/gsync"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

const securityPath = "/settings/security"

type securityView struct {
	HasPassword bool
	Google      *domain.Identity
	GoogleOn    bool
	Fresh       bool
	MinPassword int
}

func (a *Pages) securityPage(w http.ResponseWriter, r *http.Request) {
	a.renderSecurity(w, r, http.StatusOK, web.NewForm(nil))
}

func (a *Pages) renderSecurity(w http.ResponseWriter, r *http.Request, status int, f *web.Form) {
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
	a.site.Render(w, r, status, "settings/security", &web.Page{
		Title: "Security",
		Nav:   "settings/security",
		User:  &u,
		Form:  f,
		Data: securityView{
			HasPassword: u.HasPassword(),
			Google:      googleIdentity(ids),
			GoogleOn:    a.google != nil,
			Fresh:       p.Fresh(a.clock.Now()),
			MinPassword: a.cfg.Security.PasswordMinLength,
		},
	})
}

func (a *Pages) changePassword(w http.ResponseWriter, r *http.Request) {
	f, ok := a.site.ParseForm(w, r)
	if !ok {
		return
	}
	pw := r.PostForm.Get("password")
	if pw != r.PostForm.Get("password_confirm") {
		f.Errors["password_confirm"] = "Passwords do not match."
		a.renderSecurity(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	err := a.auth.ChangePassword(r.Context(), auth.PrincipalFrom(r.Context()), r.PostForm.Get("current_password"), pw, auth.MetaFrom(r))
	if err != nil {
		if errors.Is(err, auth.ErrReauthRequired) {
			a.site.Fail(w, r, err)
			return
		}
		a.site.Retry(w, r, err, f, func(status int) { a.renderSecurity(w, r, status, f) })
		return
	}
	a.site.Flash(w, r, "ok", "Password saved. Other sessions were signed out.")
	a.site.Redirect(w, r, securityPath)
}

func (a *Pages) unlinkGoogle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := auth.PrincipalFrom(ctx).UserID
	u, err := a.auth.User(ctx, user)
	if err != nil {
		a.site.Fail(w, r, err)
		return
	}
	if !u.HasPassword() {
		a.site.Flash(w, r, "error", web.ErrorText(auth.ErrLastLogin))
		a.site.Redirect(w, r, securityPath)
		return
	}
	if a.sync != nil {
		if err := a.sync.Disconnect(ctx, user, true); err != nil && !errors.Is(err, gsync.ErrNotConnected) {
			a.site.Fail(w, r, err)
			return
		}
	}
	err = a.auth.UnlinkIdentity(ctx, user, domain.ProviderGoogle, auth.MetaFrom(r))
	switch {
	case err == nil:
		a.site.Flash(w, r, "ok", "Google account unlinked.")
	case web.ErrorText(err) != "":
		a.site.Flash(w, r, "error", web.ErrorText(err))
	default:
		a.site.Fail(w, r, err)
		return
	}
	a.site.Redirect(w, r, securityPath)
}
