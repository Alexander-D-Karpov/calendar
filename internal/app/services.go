package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/config"
	"github.com/Alexander-D-Karpov/calendar/internal/dedup"
	"github.com/Alexander-D-Karpov/calendar/internal/exporter"
	"github.com/Alexander-D-Karpov/calendar/internal/google"
	"github.com/Alexander-D-Karpov/calendar/internal/gsync"
	"github.com/Alexander-D-Karpov/calendar/internal/importer"
	"github.com/Alexander-D-Karpov/calendar/internal/jobs"
	"github.com/Alexander-D-Karpov/calendar/internal/mail"
	"github.com/Alexander-D-Karpov/calendar/internal/netx"
	"github.com/Alexander-D-Karpov/calendar/internal/og"
	"github.com/Alexander-D-Karpov/calendar/internal/push"
	"github.com/Alexander-D-Karpov/calendar/internal/realtime"
	"github.com/Alexander-D-Karpov/calendar/internal/reminders"
	"github.com/Alexander-D-Karpov/calendar/internal/search"
	"github.com/Alexander-D-Karpov/calendar/internal/service"
	"github.com/Alexander-D-Karpov/calendar/internal/store/pg"
	"github.com/Alexander-D-Karpov/calendar/internal/subscription"
	"github.com/Alexander-D-Karpov/calendar/internal/undo"
)

const (
	hashConcurrency = 4
	googleTimeout   = 20 * time.Second
	pushTimeout     = 15 * time.Second
)

type services struct {
	Store      *pg.Store
	Auth       *auth.Service
	Settings   *service.Settings
	Calendars  *service.Calendars
	Events     *service.Events
	TodoLists  *service.TodoLists
	Todos      *service.Todos
	Shares     *service.Shares
	Hub        *realtime.Hub
	Listener   *realtime.Listener
	Notices    *realtime.Notices
	Worker     *jobs.Worker
	GoogleAuth *google.OAuth
	Sync       *gsync.Engine
	Import     *importer.Service
	Export     *exporter.Service
	Subs       *subscription.Service
	Dedup      *dedup.Service
	Search     *search.Service
	Push       *push.Sender
	Reminders  *reminders.Service
	Undo       *undo.Service
	Mail       *mail.Sender
	OG         *og.Renderer
	OGCache    *og.Cache
}

func (a *App) initServices() error {
	a.Store = pg.New(a.Pool)
	svc, err := NewAuth(a.Config, a.Store, a.Clock, a.Logger)
	if err != nil {
		return err
	}
	a.Auth = svc
	a.Settings = service.NewSettings(a.Store)
	a.Calendars = service.NewCalendars(a.Store)
	a.Events = service.NewEvents(a.Store, a.Store, a.Store, a.Clock, a.Config.Recurrence.MaxInstances)
	a.TodoLists = service.NewTodoLists(a.Store)
	a.Todos = service.NewTodos(a.Store, a.Store, a.Clock)
	a.Shares = service.NewShares(a.Store, a.Store, a.Keys, a.Clock)
	a.Hub = realtime.NewHub(a.Metrics.WSConnections)
	a.Listener = realtime.NewListener(a.Config.Database.URL, a.Hub, a.Logger)
	a.Notices = realtime.NewNotices(a.Config.Database.URL, a.Hub, a.Logger.With("component", "notices"))
	if c := a.Config.Google; c.Enabled() {
		a.GoogleAuth = google.NewOAuth(c.ClientID, c.ClientSecret, a.Config.App.URL(google.CallbackPath), a.Net.Client(netx.Google, googleTimeout))
		if c.SyncEnabled {
			hook := ""
			if c.WebhooksEnabled {
				hook = a.Config.App.URL(gsync.WebhookPath)
			}
			a.Sync = gsync.New(gsync.Options{
				Store:        a.Store,
				Keys:         a.Keys,
				OAuth:        a.GoogleAuth,
				Clock:        a.Clock,
				Logger:       a.Logger.With("component", "gsync"),
				Metrics:      a.Metrics,
				PollInterval: c.PollInterval,
				WebhookURL:   hook,
				Enqueue: func(ctx context.Context, kind string, payload any, o jobs.Options) error {
					return jobs.Enqueue(ctx, a.Pool, kind, payload, o)
				},
			})
		}
	}
	a.Dedup = dedup.New(dedup.Options{
		Repo:   a.Store,
		Clock:  a.Clock,
		Logger: a.Logger.With("component", "dedup"),
		Enqueue: func(ctx context.Context, kind string, payload any, o jobs.Options) error {
			return jobs.Enqueue(ctx, a.Pool, kind, payload, o)
		},
	})
	a.Import = importer.New(importer.Options{
		Repo: a.Store, Clock: a.Clock, MaxItems: a.Config.Import.MaxItems, OnCommit: a.Dedup.Scan,
	})
	a.Export = exporter.New(a.Store)
	a.Search = search.NewService(a.Store, a.Clock)
	if dir := a.Config.OG.CacheDir; dir != "" {
		renderer, err := og.New()
		if err != nil {
			return err
		}
		cache, err := og.NewCache(dir, a.Config.OG.CacheMax.Int64(), a.Metrics.OGCacheBytes)
		if err != nil {
			return err
		}
		a.OG, a.OGCache = renderer, cache
	}
	if c := a.Config.SMTP; c.Enabled() {
		a.Mail = mail.New(mail.Config{
			Host: c.Host, Port: c.Port, User: c.User, Password: c.Password, From: c.From, TLS: c.TLS,
		}, a.Logger.With("component", "mail"), a.Metrics)
	}
	a.Undo = undo.New(undo.Options{Repo: a.Store, Clock: a.Clock, Logger: a.Logger.With("component", "undo")})
	a.Subs = subscription.New(subscription.Options{
		Repo:            a.Store,
		Keys:            a.Keys,
		Client:          a.Net.Client(netx.Fetch, a.Config.Fetch.Timeout),
		Clock:           a.Clock,
		Logger:          a.Logger.With("component", "subscription"),
		DefaultInterval: a.Config.Subscription.DefaultInterval,
		MinInterval:     a.Config.Subscription.MinInterval,
		MaxSize:         a.Config.Import.MaxSize.Int64(),
		MaxItems:        a.Config.Import.MaxItems,
		Enqueue: func(ctx context.Context, kind string, payload any, o jobs.Options) error {
			return jobs.Enqueue(ctx, a.Pool, kind, payload, o)
		},
	})
	if c := a.Config.WebPush; c.Enabled() {
		sender, err := push.New(c.PublicKey, c.PrivateKey, c.Subject, a.Net.Client(netx.Push, pushTimeout), a.Clock)
		if err != nil {
			return err
		}
		a.Push = sender
	}
	a.Reminders = reminders.New(reminders.Options{
		Repo:    a.Store,
		Push:    a.Push,
		Clock:   a.Clock,
		Logger:  a.Logger.With("component", "reminders"),
		Metrics: a.Metrics,
		BaseURL: a.Config.App.Origin(),
	})
	if a.Config.Worker.Enabled {
		a.Worker = a.newWorker()
	}
	return nil
}

func NewAuth(cfg *config.Config, repo auth.Repository, clk clock.Clock, logger *slog.Logger) (*auth.Service, error) {
	hasher, err := auth.NewHasher(auth.DefaultParams, hashConcurrency)
	if err != nil {
		return nil, err
	}
	return auth.NewService(auth.Options{
		Repo:              repo,
		Hasher:            hasher,
		Clock:             clk,
		Logger:            logger,
		Policy:            auth.NewSessionPolicy(cfg.Security),
		Registration:      cfg.Registration,
		PasswordMinLength: cfg.Security.PasswordMinLength,
		LoginRate:         cfg.RateLimit.Login,
		DefaultTimezone:   cfg.App.DefaultTimezone.String(),
		VerifyEmail:       cfg.SMTP.Enabled(),
	}), nil
}
