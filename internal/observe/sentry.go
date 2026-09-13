package observe

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/getsentry/sentry-go"

	"github.com/Alexander-D-Karpov/calendar/internal/httpx"
)

type Options struct {
	DSN              string
	Environment      string
	Release          string
	TracesSampleRate float64
	HTTPTransport    http.RoundTripper
	Debug            bool
}

var enabled atomic.Bool

func Init(o Options) (func(time.Duration) bool, error) {
	noop := func(time.Duration) bool { return true }
	if o.DSN == "" {
		return noop, nil
	}
	if err := sentry.Init(clientOptions(o)); err != nil {
		return noop, fmt.Errorf("sentry: %w", err)
	}
	enabled.Store(true)
	return sentry.Flush, nil
}

func Enabled() bool {
	return enabled.Load()
}

func clientOptions(o Options) sentry.ClientOptions {
	return sentry.ClientOptions{
		Dsn:              o.DSN,
		Environment:      o.Environment,
		Release:          o.Release,
		EnableTracing:    o.TracesSampleRate > 0,
		TracesSampleRate: o.TracesSampleRate,
		HTTPTransport:    o.HTTPTransport,
		SendDefaultPII:   false,
		AttachStacktrace: true,
		MaxBreadcrumbs:   50,
		Debug:            o.Debug,
		BeforeSend: func(e *sentry.Event, _ *sentry.EventHint) *sentry.Event {
			return Scrub(e)
		},
		BeforeSendTransaction: func(e *sentry.Event, _ *sentry.EventHint) *sentry.Event {
			return Scrub(e)
		},
		BeforeBreadcrumb: func(b *sentry.Breadcrumb, _ *sentry.BreadcrumbHint) *sentry.Breadcrumb {
			scrubBreadcrumb(b)
			return b
		},
	}
}

func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !enabled.Load() {
			next.ServeHTTP(w, r)
			return
		}
		hub := sentry.CurrentHub().Clone()
		route := httpx.RouteLabel(r.Context())
		hub.ConfigureScope(func(s *sentry.Scope) {
			s.SetRequest(r)
			s.SetTag("route", route)
			if id := httpx.RequestIDFrom(r.Context()); id != "" {
				s.SetTag("request_id", id)
			}
		})
		ctx := sentry.SetHubOnContext(r.Context(), hub)

		var tx *sentry.Span
		if c := hub.Client(); c != nil && c.Options().EnableTracing {
			tx = sentry.StartTransaction(ctx, r.Method+" "+route,
				sentry.WithOpName("http.server"),
				sentry.WithTransactionSource(sentry.SourceRoute),
			)
			ctx = tx.Context()
		}
		rw, info := httpx.Record(w)
		if tx != nil {
			defer func() {
				tx.Status = sentry.HTTPtoSpanStatus(info.StatusCode())
				tx.Finish()
			}()
		}
		next.ServeHTTP(rw, r.WithContext(ctx))
	})
}

func SetUser(ctx context.Context, id string) {
	if hub := sentry.GetHubFromContext(ctx); hub != nil {
		hub.Scope().SetUser(sentry.User{ID: id})
	}
}

func WithHub(ctx context.Context, tags map[string]string) context.Context {
	if !enabled.Load() {
		return ctx
	}
	hub := sentry.CurrentHub().Clone()
	hub.ConfigureScope(func(s *sentry.Scope) {
		s.SetTags(tags)
	})
	return sentry.SetHubOnContext(ctx, hub)
}
