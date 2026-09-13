package account

import (
	"net/http"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

type sessionRow struct {
	domain.Session
	Current bool
}

func (a *Pages) sessionsPage(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFrom(r.Context())
	list, err := a.auth.ListSessions(r.Context(), p.UserID)
	if err != nil {
		a.site.Fail(w, r, err)
		return
	}
	rows := make([]sessionRow, len(list))
	for i, x := range list {
		rows[i] = sessionRow{Session: x, Current: x.ID == p.SessionID}
	}
	a.site.Render(w, r, http.StatusOK, "settings/sessions", &web.Page{Title: "Sessions", Nav: "settings/sessions", Data: rows})
}

func (a *Pages) revokeSession(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFrom(r.Context())
	id, err := domain.ParseID(r.PathValue("id"))
	if err != nil {
		a.site.Fail(w, r, domain.ErrNotFound)
		return
	}
	if id == p.SessionID {
		a.logout(w, r)
		return
	}
	if err := a.auth.RevokeSession(r.Context(), p.UserID, id, auth.MetaFrom(r)); err != nil {
		a.site.Fail(w, r, err)
		return
	}
	a.site.Flash(w, r, "ok", "The session was signed out.")
	a.site.Redirect(w, r, "/settings/sessions")
}

func (a *Pages) revokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	n, err := a.auth.RevokeOtherSessions(r.Context(), auth.PrincipalFrom(r.Context()), auth.MetaFrom(r))
	if err != nil {
		a.site.Fail(w, r, err)
		return
	}
	a.site.Flash(w, r, "ok", web.Plural(int(n), "other session")+" signed out.")
	a.site.Redirect(w, r, "/settings/sessions")
}
