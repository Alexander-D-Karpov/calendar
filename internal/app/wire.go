package app

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/api"
	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/config"
	"github.com/Alexander-D-Karpov/calendar/internal/db"
	"github.com/Alexander-D-Karpov/calendar/internal/gsync"
	"github.com/Alexander-D-Karpov/calendar/internal/httpx"
	"github.com/Alexander-D-Karpov/calendar/internal/observe"
	"github.com/Alexander-D-Karpov/calendar/internal/ratelimit"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
	"github.com/Alexander-D-Karpov/calendar/internal/web/account"
	"github.com/Alexander-D-Karpov/calendar/internal/web/apidocs"
	webcal "github.com/Alexander-D-Karpov/calendar/internal/web/calendar"
	"github.com/Alexander-D-Karpov/calendar/internal/web/googlesync"
	webhealth "github.com/Alexander-D-Karpov/calendar/internal/web/health"
	"github.com/Alexander-D-Karpov/calendar/internal/web/maintenance"
	"github.com/Alexander-D-Karpov/calendar/internal/web/notifications"
	"github.com/Alexander-D-Karpov/calendar/internal/web/searchweb"
	webshares "github.com/Alexander-D-Karpov/calendar/internal/web/shares"
	"github.com/Alexander-D-Karpov/calendar/internal/web/timeline"
	webtodos "github.com/Alexander-D-Karpov/calendar/internal/web/todos"
	"github.com/Alexander-D-Karpov/calendar/internal/web/transferweb"
	"github.com/Alexander-D-Karpov/calendar/internal/web/undoweb"
)

var (
	sessionless    = []string{"/static/", "/colors.css", "/healthz", "/readyz", "/hooks/"}
	largeBodyPaths = []string{api.Prefix + "/imports", "/settings/import"}
)

func (a *App) Handler() (http.Handler, error) {
	cfg := a.Config
	guard := auth.NewGuard(a.Auth, cfg.App.Origin(), nil).
		WithAuthFailures(ratelimit.New(cfg.RateLimit.Login, a.Clock))
	deps := web.Deps{Config: cfg, Logger: a.Logger, Auth: a.Auth, Guard: guard, Keys: a.Keys, Clock: a.Clock, Mail: a.Mail}
	if a.Sync != nil {
		deps.Banner = a.Sync.Banner
	}
	site, err := web.New(deps)
	if err != nil {
		return nil, err
	}
	apiFail := api.Fail(a.Logger)
	fail := func(w http.ResponseWriter, r *http.Request, err error) {
		if isAPI(r.URL.Path) {
			apiFail(w, r, err)
			return
		}
		site.Fail(w, r, err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", webhealth.Liveness)
	mux.Handle("GET /readyz", webhealth.Readiness(a.Logger, 3*time.Second,
		webhealth.Check{Name: "database", Run: func(ctx context.Context) error { return db.CheckSchema(ctx, a.Pool) }},
	))
	src := timeline.Source{Events: a.Events, Lists: a.TodoLists, Todos: a.Todos, Sleep: a.Store, Changes: a.Store}
	var connector account.Connector
	if a.Sync != nil {
		connector = a.Sync
	}
	account.New(site, a.Settings, a.GoogleAuth, connector).Routes(mux)
	calendarModule := webcal.New(webcal.Deps{
		Site: site, Calendars: a.Calendars, Events: a.Events, Source: src, Hub: a.Hub, Store: a.Store, Undo: a.Undo,
	})
	calendarModule.Routes(mux)
	todosModule := webtodos.New(webtodos.Deps{
		Site: site, Lists: a.TodoLists, Todos: a.Todos, Store: a.Store, Undo: a.Undo,
	})
	todosModule.Routes(mux)
	undoweb.New(undoweb.Deps{Site: site, Undo: a.Undo, Events: calendarModule, Todos: todosModule}).Routes(mux)
	webshares.New(webshares.Deps{
		Site:      site,
		Shares:    a.Shares,
		Calendars: a.Calendars,
		Source:    src,
		Hub:       a.Hub,
		Export:    a.Export,
		OG:        a.OG,
		OGCache:   a.OGCache,
		Stamps:    a.Store,
		Metrics:   a.Metrics,
		Limiter:   ratelimit.New(cfg.RateLimit.Share, a.Clock),
	}).Routes(mux)
	transferweb.New(transferweb.Deps{
		Site: site, Import: a.Import, Export: a.Export, Subs: a.Subs, Calendars: a.Calendars, Lists: a.TodoLists,
	}).Routes(mux)
	maintenance.New(maintenance.Deps{Site: site, Dedup: a.Dedup, Store: a.Store}).Routes(mux)
	notifications.New(notifications.Deps{Site: site, Store: a.Store, Sender: a.Push}).Routes(mux)
	searchweb.New(searchweb.Deps{Site: site, Search: a.Search}).Routes(mux)
	if a.Sync != nil {
		googlesync.New(googlesync.Deps{Site: site, Engine: a.Sync, Calendars: a.Calendars, Lists: a.TodoLists, Store: a.Store}).Routes(mux)
		if cfg.Google.WebhooksEnabled {
			mux.HandleFunc("POST "+gsync.WebhookPath, a.Sync.Webhook)
		}
	}
	site.Routes(mux)
	apidocs.New(site).Routes(mux)
	if err := api.Routes(mux, api.Deps{
		Settings:    a.Settings,
		Calendars:   a.Calendars,
		Events:      a.Events,
		TodoLists:   a.TodoLists,
		Todos:       a.Todos,
		Import:      a.Import,
		Export:      a.Export,
		Subs:        a.Subs,
		Dedup:       a.Dedup,
		Store:       a.Store,
		Search:      a.Search,
		Shares:      a.Shares,
		Changes:     a.Store,
		Sync:        a.Sync,
		BaseURL:     cfg.App.Origin(),
		Idempotency: a.Store,
		Guard:       guard.WithFail(apiFail),
		Limiter:     ratelimit.New(cfg.RateLimit.API, a.Clock),
		Logger:      a.Logger,
	}); err != nil {
		return nil, err
	}

	ip := httpx.NewClientIPResolver(cfg.HTTP.TrustedProxies)
	return httpx.Chain(mux,
		httpx.Timing,
		httpx.RequestID,
		ip.Middleware,
		httpx.Route(mux),
		observe.Middleware,
		a.Metrics.Middleware,
		httpx.Logging(httpx.LogOptions{
			Logger: a.Logger,
			Redact: httpx.RedactPathSegment("/s/"),
			Skip:   isProbe,
		}),
		httpx.Recover(a.Logger),
		httpx.SecurityHeaders(httpx.SecurityOptions{
			CSP:  httpx.BuildCSP(cfg.App.BaseURL),
			HSTS: cfg.App.Secure(),
		}),
		httpx.BodyLimit(bodyLimit(cfg)),
		unless(sessionless, guard.WithFail(fail).Session),
		sentryUser,
	), nil
}

func bodyLimit(cfg *config.Config) func(*http.Request) int64 {
	small := cfg.HTTP.MaxBody.Int64()
	large := cfg.Import.MaxSize.Int64() + 64<<10
	return func(r *http.Request) int64 {
		if r.Method == http.MethodPost && slices.Contains(largeBodyPaths, r.URL.Path) {
			return large
		}
		return small
	}
}

func isAPI(path string) bool {
	return strings.HasPrefix(path, "/api/") && !strings.HasPrefix(path, "/api/docs")
}

func unless(prefixes []string, mw httpx.Middleware) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		wrapped := mw(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, p := range prefixes {
				if strings.HasPrefix(r.URL.Path, p) {
					next.ServeHTTP(w, r)
					return
				}
			}
			wrapped.ServeHTTP(w, r)
		})
	}
}

func sentryUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := auth.PrincipalFrom(r.Context()); p != nil {
			observe.SetUser(r.Context(), p.UserID.String())
		}
		next.ServeHTTP(w, r)
	})
}

func isProbe(r *http.Request) bool {
	return r.URL.Path == "/healthz" || r.URL.Path == "/readyz"
}
