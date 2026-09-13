package web

import (
	"net/http"
	"net/mail"
	"strings"
)

type legalView struct {
	AppName string
	Host    string
	Contact string
	Google  bool
	Sync    bool
	Sentry  bool
}

func (s *Server) legal() legalView {
	v := legalView{
		AppName: s.cfg.App.Name,
		Contact: contactAddress(s.cfg.SMTP.From),
		Google:  s.cfg.Google.Enabled(),
		Sync:    s.cfg.Google.SyncActive(),
		Sentry:  s.cfg.Sentry.DSN != "",
	}
	if s.cfg.App.BaseURL != nil {
		v.Host = s.cfg.App.BaseURL.Host
	}
	return v
}

// contactAddress pulls the bare address out of an RFC 5322 From header so the
// policy shows "calendar@example.com", not `Calendar <calendar@example.com>`.
func contactAddress(from string) string {
	from = strings.TrimSpace(from)
	if from == "" {
		return ""
	}
	if a, err := mail.ParseAddress(from); err == nil {
		return a.Address
	}
	return from
}

func (s *Server) privacy(w http.ResponseWriter, r *http.Request) {
	s.Render(w, r, http.StatusOK, "privacy", &Page{Title: "Privacy", Data: s.legal()})
}

func (s *Server) terms(w http.ResponseWriter, r *http.Request) {
	s.Render(w, r, http.StatusOK, "terms", &Page{Title: "Terms", Data: s.legal()})
}
