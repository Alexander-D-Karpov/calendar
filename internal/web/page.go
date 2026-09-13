package web

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/buildinfo"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/httpx"
)

const repoURL = "https://github.com/Alexander-D-Karpov/calendar"

type Footer struct {
	AppName     string
	Version     string
	VersionURL  string
	Commit      string
	CommitShort string
	CommitURL   string
	Modified    bool
	Built       string
	GoVersion   string
	RepoURL     string
}

type Page struct {
	Title            string
	Nav              string
	User             *domain.User
	CSRF             string
	Theme            string
	Path             string
	Flash            *Flash
	Banner           string
	Form             *Form
	Data             any
	Footer           *Footer
	RegistrationOpen bool
	GoogleLogin      bool
	GoogleSignup     bool
	GoogleSync       bool
	SMTPEnabled      bool
	Fragment         bool

	started  time.Time
	tplStart time.Time
	loc      *time.Location
}

func (p *Page) PageTime() string {
	return formatMS(time.Since(p.started))
}

func (p *Page) TemplateTime() string {
	return formatMS(time.Since(p.tplStart))
}

func (p *Page) InSettings() bool {
	return strings.HasPrefix(p.Nav, "settings/")
}

func (p *Page) Time(v any) string {
	var t time.Time
	switch x := v.(type) {
	case time.Time:
		t = x
	case *time.Time:
		if x == nil {
			return ""
		}
		t = *x
	default:
		return ""
	}
	return t.In(p.location()).Format("2006-01-02 15:04")
}

func (p *Page) location() *time.Location {
	if p.loc == nil {
		return time.UTC
	}
	return p.loc
}

const FragmentHeader = "X-Fragment"

const FlashHeader = "X-Flash"

func IsFragment(r *http.Request) bool {
	return r.Header.Get(FragmentHeader) == "1"
}

func Block(r *http.Request, name string) string {
	if IsFragment(r) {
		return name
	}
	return ""
}

func (s *Server) Render(w http.ResponseWriter, r *http.Request, status int, name string, p *Page) {
	s.RenderBlock(w, r, status, name, "", p)
}

func (s *Server) RenderBlock(w http.ResponseWriter, r *http.Request, status int, name, block string, p *Page) {
	s.fill(w, r, p)
	var buf bytes.Buffer
	p.tplStart = time.Now()
	var err error
	if block == "" {
		err = s.render.Execute(&buf, name, p)
	} else {
		err = s.render.ExecuteBlock(&buf, name, block, p)
	}
	if err != nil {
		s.logger.LogAttrs(r.Context(), slog.LevelError, "render failed", slog.String("page", name), slog.String("block", block), slog.Any("err", err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	end := time.Now()
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Vary", FragmentHeader)
	h.Set("Server-Timing", fmt.Sprintf("tpl;dur=%.1f, total;dur=%.1f", msf(end.Sub(p.tplStart)), msf(end.Sub(p.started))))
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func (s *Server) Finish(w http.ResponseWriter, r *http.Request, target string) {
	if IsFragment(r) {
		w.Header().Set("X-Location", SafeNext(target))
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.Redirect(w, r, target)
}

func (s *Server) fill(w http.ResponseWriter, r *http.Request, p *Page) {
	p.Footer = s.footer
	p.RegistrationOpen = s.RegistrationOpen()
	p.GoogleLogin = s.cfg.Google.Enabled()
	p.GoogleSignup = p.GoogleLogin && s.cfg.Registration.Enabled && s.cfg.Registration.GoogleEnabled
	p.GoogleSync = s.cfg.Google.SyncActive()
	p.SMTPEnabled = s.cfg.SMTP.Enabled()
	p.Theme = themeFrom(r)
	p.Path = currentPath(r)
	p.Fragment = IsFragment(r)
	p.loc = s.cfg.App.DefaultTimezone
	p.started = httpx.StartedAt(r.Context())
	if p.started.IsZero() {
		p.started = time.Now()
	}
	if pr := auth.PrincipalFrom(r.Context()); pr.IsSession() {
		p.CSRF = auth.CSRFToken(pr.CSRFSecret)
		if p.User == nil {
			u, err := s.auth.User(r.Context(), pr.UserID)
			if err != nil {
				s.logger.LogAttrs(r.Context(), slog.LevelWarn, "load user failed", slog.Any("err", err))
			} else {
				p.User = &u
			}
		}
	}
	if p.User != nil {
		if loc, err := time.LoadLocation(p.User.Timezone); err == nil {
			p.loc = loc
		}
		if s.banner != nil && !p.Fragment {
			p.Banner = s.banner(r.Context(), p.User.ID)
		}
	}
	if p.Flash == nil && !p.Fragment {
		p.Flash = s.flash.Pop(w, r)
	}
	if p.Form == nil {
		p.Form = NewForm(nil)
	}
}

func newFooter(appName string, info buildinfo.Info) *Footer {
	f := &Footer{
		AppName:     appName,
		Version:     info.Version,
		Commit:      info.Commit,
		CommitShort: info.ShortCommit(),
		Modified:    info.Modified,
		GoVersion:   goVersion(info.GoVersion),
		RepoURL:     repoURL,
	}
	if info.Commit != "" {
		f.CommitURL = repoURL + "/commit/" + info.Commit
	}
	if isRelease(info.Version) {
		f.VersionURL = repoURL + "/releases/tag/" + info.Version
	}
	if t, err := time.Parse(time.RFC3339, info.Date); err == nil {
		f.Built = t.UTC().Format("2006-01-02")
	}
	return f
}

func isRelease(v string) bool {
	return strings.HasPrefix(v, "v") && !strings.Contains(v, "-") && strings.Count(v, ".") == 2
}

func goVersion(v string) string {
	v = strings.TrimPrefix(v, "go")
	if i := strings.IndexAny(v, " -"); i >= 0 {
		v = v[:i]
	}
	return "Go " + v
}

func formatMS(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return "<1ms"
	case d < 10*time.Millisecond:
		return strconv.FormatFloat(msf(d), 'f', 1, 64) + "ms"
	}
	return strconv.FormatInt(d.Milliseconds(), 10) + "ms"
}

func msf(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}
