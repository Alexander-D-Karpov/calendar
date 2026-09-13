package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Alexander-D-Karpov/calendar/internal/buildinfo"
	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/config"
	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
	"github.com/Alexander-D-Karpov/calendar/internal/db"
	"github.com/Alexander-D-Karpov/calendar/internal/logging"
	"github.com/Alexander-D-Karpov/calendar/internal/metrics"
	"github.com/Alexander-D-Karpov/calendar/internal/netx"
	"github.com/Alexander-D-Karpov/calendar/internal/observe"
	"github.com/Alexander-D-Karpov/calendar/internal/term"
)

type Options struct {
	Color   term.ColorMode
	Stderr  io.Writer
	Migrate bool
}

type App struct {
	Config  *config.Config
	Logger  *slog.Logger
	Clock   clock.Clock
	Keys    *crypto.KeyRing
	Net     *netx.Network
	Pool    *pgxpool.Pool
	Metrics *metrics.Metrics
	services

	flush func(time.Duration) bool
}

func New(ctx context.Context, cfg *config.Config, o Options) (*App, error) {
	a := &App{Config: cfg, Clock: clock.New()}

	netw, err := netx.New(netx.Options{
		Proxy:        cfg.Outbound.Proxy,
		ProxyFor:     purposes(cfg.Outbound.ProxyFor),
		NoProxy:      cfg.Outbound.NoProxy,
		AllowPrivate: cfg.Fetch.AllowPrivate,
		DialTimeout:  cfg.Fetch.Timeout,
	})
	if err != nil {
		return nil, err
	}
	a.Net = netw

	flush, sentryErr := observe.Init(observe.Options{
		DSN:              cfg.Sentry.DSN,
		Environment:      cfg.Sentry.Environment,
		Release:          buildinfo.Get().Release(),
		TracesSampleRate: cfg.Sentry.TracesSampleRate,
		HTTPTransport:    netw.Transport(netx.Sentry),
	})
	a.flush = flush
	a.Logger = newLogger(cfg, o)
	slog.SetDefault(a.Logger)
	if sentryErr != nil {
		a.Logger.Warn("sentry disabled", "err", sentryErr)
	}

	keys, err := crypto.NewKeyRing(cfg.Security.SecretKeys, cfg.Security.SecretKeyActive)
	if err != nil {
		a.Close()
		return nil, err
	}
	a.Keys = keys

	if o.Migrate {
		start := time.Now()
		if err := db.MigrateUp(ctx, cfg.Database.URL, 0); err != nil {
			a.Close()
			return nil, err
		}
		a.Logger.Info("migrations applied", "took", time.Since(start))
	}

	pool, err := db.Open(ctx, cfg.Database.URL, cfg.Database.MaxConns)
	if err != nil {
		a.Close()
		return nil, err
	}
	a.Pool = pool
	if err := db.CheckSchema(ctx, pool); err != nil {
		a.Close()
		return nil, fmt.Errorf("%w, run: calendar migrate up", err)
	}

	a.Metrics = metrics.New()
	if err := a.Metrics.RegisterPool(pool); err != nil {
		a.Close()
		return nil, err
	}

	if err := a.initServices(); err != nil {
		a.Close()
		return nil, err
	}
	return a, nil
}

func (a *App) Close() {
	if a.Pool != nil {
		a.Pool.Close()
	}
	if a.Net != nil {
		a.Net.CloseIdleConnections()
	}
	if a.flush != nil {
		a.flush(2 * time.Second)
	}
}

func newLogger(cfg *config.Config, o Options) *slog.Logger {
	w := o.Stderr
	if w == nil {
		w = os.Stderr
	}
	color := o.Color
	switch cfg.Log.Color {
	case config.ColorAlways:
		color = term.ColorAlways
	case config.ColorNever:
		color = term.ColorNever
	}
	var h slog.Handler = logging.NewHandler(logging.Options{
		Level:  cfg.Log.Level,
		Format: cfg.Log.Format,
		Color:  color,
		Writer: w,
	})
	if observe.Enabled() {
		h = logging.Tee(h, observe.NewSlogHandler(slog.LevelError))
	}
	return slog.New(h)
}

func purposes(in []string) []netx.Purpose {
	out := make([]netx.Purpose, len(in))
	for i, p := range in {
		out[i] = netx.Purpose(p)
	}
	return out
}
