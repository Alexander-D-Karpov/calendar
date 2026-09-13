package web

import (
	"net/http"
	"slices"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const themeCookie = "theme"

var themes = []string{"system", "light", "dark"}

func themeFrom(r *http.Request) string {
	if c, err := r.Cookie(themeCookie); err == nil && slices.Contains(themes, c.Value) {
		return c.Value
	}
	return "system"
}

// currentPath is what the theme form posts back as "next". Pages that hide the
// Referer, such as a public share, have nothing else to return the viewer to.
func currentPath(r *http.Request) string {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return ""
	}
	return SafeNext(r.URL.RequestURI())
}

func (s *Server) setTheme(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.Fail(w, r, domain.ErrInvalid)
		return
	}
	v := r.PostForm.Get("theme")
	if !slices.Contains(themes, v) {
		s.Fail(w, r, domain.ErrInvalid)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     themeCookie,
		Value:    v,
		Path:     "/",
		MaxAge:   365 * 24 * 3600,
		HttpOnly: true,
		Secure:   s.cfg.Security.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	back := s.refererPath(r)
	if back == "/" {
		if next := SafeNext(r.PostForm.Get("next")); next != "/" {
			back = next
		}
	}
	s.Redirect(w, r, back)
}
