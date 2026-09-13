package web

import (
	"net/http"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/view"
)

type Viewer struct {
	User domain.User
	Loc  *time.Location
	Now  time.Time
	CSRF string
}

func (s *Server) Viewer(r *http.Request) (Viewer, error) {
	p := auth.PrincipalFrom(r.Context())
	if p == nil {
		return Viewer{}, domain.ErrUnauthorized
	}
	u, err := s.auth.User(r.Context(), p.UserID)
	if err != nil {
		return Viewer{}, err
	}
	v := Viewer{User: u, Loc: u.Location(), Now: s.clock.Now()}
	if p.IsSession() {
		v.CSRF = auth.CSRFToken(p.CSRFSecret)
	}
	return v, nil
}

func (v Viewer) Today() time.Time {
	return view.Date(v.Now.In(v.Loc))
}

func (v Viewer) Clock24() bool {
	return v.User.TimeFormat != "12h"
}

func (v Viewer) Page(nav, title string, data any) *Page {
	return &Page{Title: title, Nav: nav, User: &v.User, Data: data}
}
