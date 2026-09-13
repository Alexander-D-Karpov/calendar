package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Alexander-D-Karpov/calendar/internal/jobs"
	"github.com/Alexander-D-Karpov/calendar/internal/store/pg"
)

const (
	cleanupEvery      = time.Hour
	failedJobsKeepFor = 7 * 24 * time.Hour
)

func (a *App) newWorker() *jobs.Worker {
	w := jobs.NewWorker(jobs.WorkerOptions{
		Pool:        a.Pool,
		URL:         a.Config.Database.URL,
		Concurrency: a.Config.Worker.Concurrency,
		Logger:      a.Logger.With("component", "worker"),
		Metrics:     a.Metrics,
	})
	w.Handle(jobs.KindCleanup, a.cleanup)
	w.Every(jobs.KindCleanup, cleanupEvery)
	w.Handle(jobs.KindReminders, a.Reminders.Handle)
	w.Every(jobs.KindReminders, time.Minute)
	if a.Sync != nil {
		w.Handle(jobs.KindGooglePull, a.Sync.HandlePull)
		w.Handle(jobs.KindGooglePush, a.Sync.HandlePush)
		w.Handle(jobs.KindGooglePoll, a.Sync.HandlePoll)
		w.Handle(jobs.KindGoogleWatch, a.Sync.HandleWatch)
		w.Every(jobs.KindGooglePoll, time.Minute)
		w.Every(jobs.KindGoogleWatch, time.Hour)
	}
	w.Handle(jobs.KindSubscription, a.Subs.HandleRefresh)
	w.Handle(jobs.KindSubsDue, a.Subs.HandleDue)
	w.Every(jobs.KindSubsDue, time.Minute)
	if a.OGCache != nil {
		w.Handle(jobs.KindOGEvict, a.evictOG)
		w.Every(jobs.KindOGEvict, time.Hour)
	}
	w.Handle(jobs.KindDedupScan, a.Dedup.HandleScan)
	w.Handle(jobs.KindDedupSweep, a.Dedup.HandleSweep)
	w.Every(jobs.KindDedupSweep, 24*time.Hour)
	return w
}

func (a *App) cleanup(ctx context.Context, _ jobs.Job) error {
	now := a.Clock.Now().UTC()
	r := a.Config.Retention
	if n, err := a.Store.BackfillFingerprints(ctx, 5000); err != nil {
		return err
	} else if n > 0 {
		a.Logger.LogAttrs(ctx, slog.LevelInfo, "fingerprints backfilled", slog.Int("count", n))
	}
	stats, err := a.Store.Purge(ctx, pg.Cutoffs{
		Now:     now,
		Trash:   now.Add(-r.Trash),
		Changes: now.Add(-r.Changes),
		Audit:   now.Add(-r.Audit),
		Jobs:    now.Add(-failedJobsKeepFor),
	})
	attrs := make([]slog.Attr, 0, len(stats))
	for table, n := range stats {
		if n > 0 {
			attrs = append(attrs, slog.Int64(table, n))
		}
	}
	a.Logger.LogAttrs(ctx, slog.LevelInfo, "cleanup finished", slog.Any("purged", slog.GroupValue(attrs...)))
	return err
}

func (a *App) RunWorker(ctx context.Context) error {
	if a.Worker == nil {
		return errors.New("the worker is disabled, set WORKER_ENABLED=true")
	}
	g, gctx := errgroup.WithContext(ctx)
	if addr := a.Config.Metrics.Addr; addr != "" {
		ms := a.Metrics.Server(addr, a.Config.Metrics.Token)
		ms.ErrorLog = a.errorLog()
		g.Go(func() error {
			if err := ms.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		})
		g.Go(func() error {
			<-gctx.Done()
			sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			return ms.Shutdown(sctx)
		})
	}
	g.Go(func() error {
		a.Worker.Run(gctx)
		return nil
	})
	return g.Wait()
}

func (a *App) evictOG(ctx context.Context, _ jobs.Job) error {
	n, err := a.OGCache.Evict()
	if err != nil {
		return err
	}
	a.Logger.LogAttrs(ctx, slog.LevelDebug, "og cache swept", slog.Int64("bytes", n))
	return nil
}
