package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"math/rand/v2"
	"os"
	"runtime/debug"
	"slices"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Alexander-D-Karpov/calendar/internal/crypto"
	"github.com/Alexander-D-Karpov/calendar/internal/db"
	"github.com/Alexander-D-Karpov/calendar/internal/metrics"
	"github.com/Alexander-D-Karpov/calendar/internal/observe"
)

const (
	channel      = "calendar_jobs"
	pollEvery    = 5 * time.Second
	maintainGap  = time.Minute
	scheduleGap  = time.Minute
	lockTimeout  = 15 * time.Minute
	jobTimeout   = 10 * time.Minute
	drainTimeout = 20 * time.Second
	baseBackoff  = 10 * time.Second
	maxBackoff   = time.Hour
	maxErrorLen  = 1000
)

const (
	claimSQL = `UPDATE jobs SET status = 'running', locked_by = $1, locked_at = now(), attempts = attempts + 1, updated_at = now()
	WHERE id = (
		SELECT id FROM jobs WHERE status = 'queued' AND run_at <= now() AND kind = ANY($2)
		ORDER BY priority DESC, run_at FOR UPDATE SKIP LOCKED LIMIT 1
	)
	RETURNING id, kind, payload, attempts, max_attempts`

	completeSQL = `DELETE FROM jobs WHERE id = $1`

	failSQL = `UPDATE jobs SET status = $2, last_error = $3, run_at = $4, locked_by = NULL, locked_at = NULL, updated_at = now()
	WHERE id = $1`

	dropStaleSQL = `DELETE FROM jobs j WHERE j.status = 'running' AND j.locked_at < $1 AND j.unique_key IS NOT NULL
	AND EXISTS (SELECT 1 FROM jobs q WHERE q.status = 'queued' AND q.kind = j.kind AND q.unique_key = j.unique_key)`

	reapSQL = `UPDATE jobs SET status = 'queued', locked_by = NULL, locked_at = NULL, run_at = now(),
	last_error = 'worker stopped responding', updated_at = now()
	WHERE status = 'running' AND locked_at < $1`

	countSQL = `SELECT status, count(*) FROM jobs GROUP BY status`
)

type WorkerOptions struct {
	Pool        *pgxpool.Pool
	URL         string
	Concurrency int
	Logger      *slog.Logger
	Metrics     *metrics.Metrics
}

type periodic struct {
	kind  string
	every time.Duration
}

type Worker struct {
	pool     *pgxpool.Pool
	url      string
	id       string
	n        int
	logger   *slog.Logger
	metrics  *metrics.Metrics
	handlers map[string]Handler
	periodic []periodic
	wake     chan struct{}
}

func NewWorker(o WorkerOptions) *Worker {
	host, _ := os.Hostname()
	return &Worker{
		pool:     o.Pool,
		url:      o.URL,
		id:       host + "/" + crypto.RandomBase62(8),
		n:        max(1, o.Concurrency),
		logger:   o.Logger,
		metrics:  o.Metrics,
		handlers: map[string]Handler{},
		wake:     make(chan struct{}, 1),
	}
}

func (w *Worker) Handle(kind string, h Handler) {
	w.handlers[kind] = h
}

func (w *Worker) Every(kind string, every time.Duration) {
	w.periodic = append(w.periodic, periodic{kind: kind, every: every})
}

func (w *Worker) Run(ctx context.Context) {
	kinds := slices.Sorted(maps.Keys(w.handlers))
	hard, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	stop := context.AfterFunc(ctx, func() { time.AfterFunc(drainTimeout, cancel) })
	defer stop()

	var wg sync.WaitGroup
	wg.Go(func() {
		db.Listen(ctx, w.url, channel, w.logger, func(bool) { w.signal() }, func(string) { w.signal() })
	})
	wg.Go(func() { w.schedule(ctx) })
	wg.Go(func() { w.maintain(ctx) })
	for range w.n {
		wg.Go(func() { w.loop(ctx, hard, kinds) })
	}
	w.logger.LogAttrs(ctx, slog.LevelInfo, "worker started", slog.String("id", w.id), slog.Int("concurrency", w.n), slog.Any("kinds", kinds))
	wg.Wait()
}

func (w *Worker) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *Worker) loop(ctx, hard context.Context, kinds []string) {
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	for ctx.Err() == nil {
		j, ok, err := w.claim(ctx, kinds)
		switch {
		case err != nil && ctx.Err() == nil:
			w.logger.LogAttrs(ctx, slog.LevelWarn, "claim job failed", slog.Any("err", err))
		case ok:
			w.signal()
			w.run(hard, j)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
		case <-tick.C:
		}
	}
}

func (w *Worker) claim(ctx context.Context, kinds []string) (Job, bool, error) {
	var j Job
	var attempts, maxAttempts int32
	err := w.pool.QueryRow(ctx, claimSQL, w.id, kinds).Scan(&j.ID, &j.Kind, &j.Payload, &attempts, &maxAttempts)
	if db.IsNotFound(err) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	j.Attempts, j.MaxAttempts = int(attempts), int(maxAttempts)
	return j, true, nil
}

func (w *Worker) run(hard context.Context, j Job) {
	ctx, cancel := context.WithTimeout(hard, jobTimeout)
	ctx = observe.WithHub(ctx, map[string]string{"job": j.Kind})
	start := time.Now()
	err := w.call(ctx, j)
	cancel()
	w.observe(j.Kind, err, time.Since(start))

	dctx, dcancel := context.WithTimeout(context.WithoutCancel(hard), 5*time.Second)
	defer dcancel()
	if err == nil {
		if _, derr := w.pool.Exec(dctx, completeSQL, j.ID); derr != nil {
			w.logger.LogAttrs(dctx, slog.LevelWarn, "complete job failed", slog.Int64("id", j.ID), slog.Any("err", derr))
		}
		return
	}
	final := j.Attempts >= j.MaxAttempts || IsPermanent(err)
	level := slog.LevelWarn
	if final {
		level = slog.LevelError
	}
	w.logger.LogAttrs(ctx, level, "job failed", slog.String("job", j.Kind), slog.Int64("id", j.ID),
		slog.Int("attempt", j.Attempts), slog.Bool("final", final), slog.Any("err", err))
	if ferr := w.fail(dctx, j, err, final); ferr != nil {
		w.logger.LogAttrs(dctx, slog.LevelWarn, "record job failure failed", slog.Int64("id", j.ID), slog.Any("err", ferr))
	}
}

func (w *Worker) call(ctx context.Context, j Job) (err error) {
	defer func() {
		if p := recover(); p != nil {
			w.logger.LogAttrs(ctx, slog.LevelError, "job panicked", slog.String("job", j.Kind), slog.Any("panic", p), slog.String("stack", string(debug.Stack())))
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	h, ok := w.handlers[j.Kind]
	if !ok {
		return Permanent(fmt.Errorf("no handler for %s", j.Kind))
	}
	return h(ctx, j)
}

func (w *Worker) fail(ctx context.Context, j Job, cause error, final bool) error {
	status := "queued"
	if final {
		status = "failed"
	}
	msg := observe.ScrubString(cause.Error())
	if utf8.RuneCountInString(msg) > maxErrorLen {
		msg = string([]rune(msg)[:maxErrorLen])
	}
	_, err := w.pool.Exec(ctx, failSQL, j.ID, status, msg, time.Now().Add(backoff(j.Attempts)))
	if db.IsUniqueViolation(err, "jobs_unique_key_idx") {
		_, err = w.pool.Exec(ctx, completeSQL, j.ID)
	}
	return err
}

func (w *Worker) schedule(ctx context.Context) {
	if len(w.periodic) == 0 {
		return
	}
	next := make([]time.Time, len(w.periodic))
	tick := time.NewTicker(scheduleGap)
	defer tick.Stop()
	for {
		now := time.Now()
		for i, p := range w.periodic {
			if now.Before(next[i]) {
				continue
			}
			if err := Enqueue(ctx, w.pool, p.kind, nil, Options{UniqueKey: p.kind}); err != nil {
				if ctx.Err() == nil {
					w.logger.LogAttrs(ctx, slog.LevelWarn, "schedule job failed", slog.String("job", p.kind), slog.Any("err", err))
				}
				continue
			}
			next[i] = now.Add(p.every)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (w *Worker) maintain(ctx context.Context) {
	tick := time.NewTicker(maintainGap)
	defer tick.Stop()
	for {
		w.reap(ctx)
		w.gauge(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (w *Worker) reap(ctx context.Context) {
	cutoff := time.Now().Add(-lockTimeout)
	for _, sql := range []string{dropStaleSQL, reapSQL} {
		tag, err := w.pool.Exec(ctx, sql, cutoff)
		switch {
		case err != nil && ctx.Err() == nil:
			w.logger.LogAttrs(ctx, slog.LevelWarn, "reap jobs failed", slog.Any("err", err))
		case err == nil && tag.RowsAffected() > 0:
			w.logger.LogAttrs(ctx, slog.LevelWarn, "reaped stale jobs", slog.Int64("count", tag.RowsAffected()))
		}
	}
}

func (w *Worker) gauge(ctx context.Context) {
	if w.metrics == nil {
		return
	}
	rows, err := w.pool.Query(ctx, countSQL)
	if err != nil {
		return
	}
	defer rows.Close()
	counts := map[string]float64{"queued": 0, "running": 0, "failed": 0}
	for rows.Next() {
		var status string
		var n int64
		if rows.Scan(&status, &n) == nil {
			counts[status] = float64(n)
		}
	}
	for status, n := range counts {
		w.metrics.JobQueue.WithLabelValues(status).Set(n)
	}
}

func (w *Worker) observe(kind string, err error, d time.Duration) {
	if w.metrics == nil {
		return
	}
	w.metrics.Jobs.WithLabelValues(kind, metrics.Result(err)).Inc()
	w.metrics.JobDuration.WithLabelValues(kind).Observe(d.Seconds())
}

func backoff(attempt int) time.Duration {
	d := min(baseBackoff<<min(max(attempt-1, 0), 12), maxBackoff)
	return d/2 + rand.N(d/2+1)
}
