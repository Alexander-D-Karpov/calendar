package web

import (
	"context"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/buildinfo"
	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/config"
	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/httpx"
	"github.com/Alexander-D-Karpov/calendar/internal/mail"
	"github.com/Alexander-D-Karpov/calendar/internal/markdown"
	webfs "github.com/Alexander-D-Karpov/calendar/web"
)

type Deps struct {
	Config *config.Config
	Logger *slog.Logger
	Auth   *auth.Service
	Guard  *auth.Guard
	Keys   *crypto.KeyRing
	Clock  clock.Clock
	Banner func(ctx context.Context, user domain.ID) string
	Mail   *mail.Sender
}

type Server struct {
	cfg    *config.Config
	logger *slog.Logger
	auth   *auth.Service
	guard  *auth.Guard
	clock  clock.Clock
	keys   *crypto.KeyRing
	render *Renderer
	assets *Assets
	flash  flasher
	footer *Footer
	banner func(ctx context.Context, user domain.ID) string
	mail   *mail.Sender
}

func New(d Deps) (*Server, error) {
	dev := d.Config.App.IsDevelopment()
	render, assets, err := buildRenderer(contentFS(dev), dev)
	if err != nil {
		return nil, err
	}
	if d.Clock == nil {
		d.Clock = clock.New()
	}
	s := &Server{
		cfg:    d.Config,
		logger: d.Logger,
		auth:   d.Auth,
		clock:  d.Clock,
		keys:   d.Keys,
		render: render,
		assets: assets,
		flash:  flasher{keys: d.Keys, secure: d.Config.Security.CookieSecure},
		footer: newFooter(d.Config.App.Name, buildinfo.Get()),
		banner: d.Banner,
		mail:   d.Mail,
	}
	s.guard = d.Guard.WithFail(s.Fail)
	return s, nil
}

func EmbeddedRenderer() (*Renderer, error) {
	r, _, err := buildRenderer(webfs.FS, false)
	return r, err
}

func buildRenderer(root fs.FS, dev bool) (*Renderer, *Assets, error) {
	static, err := fs.Sub(root, "static")
	if err != nil {
		return nil, nil, err
	}
	templates, err := fs.Sub(root, "templates")
	if err != nil {
		return nil, nil, err
	}
	assets, err := NewAssets(static, dev)
	if err != nil {
		return nil, nil, err
	}
	render, err := NewRenderer(templates, dev, template.FuncMap{
		"asset":    assets.URL,
		"ua":       describeUA,
		"markdown": markdown.Render,
		"colors":   colorsHref,
	})
	if err != nil {
		return nil, nil, err
	}
	return render, assets, nil
}

func (s *Server) Routes(mux *http.ServeMux) {
	mux.Handle("GET /static/{path...}", s.assets)
	mux.Handle("GET /sw.js", s.assets.ServeFile("js/sw.js"))
	mux.HandleFunc("GET /colors.css", colorsCSS)
	mux.Handle("POST /theme", s.Handle(s.setTheme))
	mux.Handle("/", s.Handle(s.notFound))
}

// SendMail never fails a request: a bounced link is recoverable, a 500 is not.
func (s *Server) SendMail(ctx context.Context, kind, to, subject, body string) {
	if err := s.mail.Send(ctx, kind, to, subject, body); err != nil {
		s.logger.LogAttrs(ctx, slog.LevelWarn, "send mail failed", slog.String("kind", kind), slog.Any("err", err))
	}
}

func (s *Server) HasAsset(name string) bool {
	return s.assets.Has(name)
}

func (s *Server) Handle(h http.HandlerFunc, mw ...httpx.Middleware) http.Handler {
	return httpx.Chain(h, append([]httpx.Middleware{s.guard.CSRF}, mw...)...)
}

func (s *Server) RequireUser(next http.Handler) http.Handler {
	return s.guard.RequireUser(next)
}

func (s *Server) RequireFresh(next http.Handler) http.Handler {
	return s.guard.RequireFresh(next)
}

func (s *Server) Auth() *auth.Service {
	return s.auth
}

func (s *Server) Guard() *auth.Guard {
	return s.guard
}

func (s *Server) Config() *config.Config {
	return s.cfg
}

func (s *Server) Clock() clock.Clock {
	return s.clock
}

func (s *Server) Logger() *slog.Logger {
	return s.logger
}

func (s *Server) RegistrationOpen() bool {
	return s.cfg.Registration.Enabled && s.cfg.Registration.PasswordEnabled
}

func (s *Server) Flash(w http.ResponseWriter, r *http.Request, kind, text string) {
	s.FlashUndo(w, r, kind, text, "")
}

func (s *Server) FlashUndo(w http.ResponseWriter, r *http.Request, kind, text, undo string) {
	if IsFragment(r) {
		w.Header().Set(FlashHeader, kind+":"+undo+":"+url.PathEscape(text))
		return
	}
	s.flash.Set(w, kind, text, undo)
}

func contentFS(dev bool) fs.FS {
	if dev {
		if st, err := os.Stat("web/templates"); err == nil && st.IsDir() {
			return os.DirFS("web")
		}
	}
	return webfs.FS
}

func RedirectTo(target string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target, http.StatusSeeOther)
	}
}
