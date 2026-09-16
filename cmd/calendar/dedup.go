package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/db"
	"github.com/Alexander-D-Karpov/calendar/internal/dedup"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/jobs"
	"github.com/Alexander-D-Karpov/calendar/internal/store/pg"

	"github.com/jackc/pgx/v5/pgxpool"
)

func dedupCommand() *command {
	return &command{
		name:    "dedup",
		summary: "Review and resolve duplicate events and todos",
		sub: []*command{
			{name: "scan", summary: "Re-scan a user's calendar for duplicates", args: "<email>", setup: dedupScan},
			{name: "list", summary: "Show pending duplicates for a user", args: "<email>", setup: dedupList},
			{name: "resolve", summary: "Resolve pending duplicates, keeping the copy Google is linked to", args: "<email>", setup: dedupResolve},
		},
	}
}

type dedupReport struct {
	ID     string  `json:"id"`
	Entity string  `json:"entity"`
	Reason string  `json:"reason"`
	Score  float64 `json:"score"`
	Keep   string  `json:"keep,omitempty"`
	Drop   string  `json:"drop,omitempty"`
	Why    string  `json:"why,omitempty"`
}

func (a *app) dedupService(ctx context.Context) (*dedup.Service, *pgxpool.Pool, func(), error) {
	cfg, err := a.config()
	if err != nil {
		return nil, nil, nil, err
	}
	pool, err := db.Open(ctx, cfg.Database.URL, 2)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := db.CheckSchema(ctx, pool); err != nil {
		pool.Close()
		return nil, nil, nil, fmt.Errorf("%w, run: calendar migrate up", err)
	}
	svc := dedup.New(dedup.Options{
		Repo: pg.New(pool), Clock: clock.New(), Logger: slog.New(slog.DiscardHandler),
		Enqueue: func(ctx context.Context, kind string, payload any, o jobs.Options) error {
			return jobs.Enqueue(ctx, pool, kind, payload, o)
		},
	})
	return svc, pool, pool.Close, nil
}

// side describes how strongly a copy is tied to Google. An event-level mapping
// is the strongest signal; failing that, living in a calendar that is bound to
// Google still means Google is the side to preserve, because the copy in an
// unsynced calendar is the stray one.
type side struct{ mapped, bound bool }

func sides(ctx context.Context, pool *pgxpool.Pool, owner domain.ID, d domain.Duplicate) (a, b side, err error) {
	const q = `SELECT
	    EXISTS (SELECT 1 FROM sync_mappings m JOIN sync_bindings sb ON sb.id = m.binding_id
	            WHERE sb.owner_id = $1 AND m.entity = $2 AND m.local_id = $3),
	    EXISTS (SELECT 1 FROM sync_mappings m JOIN sync_bindings sb ON sb.id = m.binding_id
	            WHERE sb.owner_id = $1 AND m.entity = $2 AND m.local_id = $4),
	    EXISTS (SELECT 1 FROM events e JOIN sync_bindings sb ON sb.local_calendar_id = e.calendar_id
	            WHERE e.id = $3 AND sb.owner_id = $1),
	    EXISTS (SELECT 1 FROM events e JOIN sync_bindings sb ON sb.local_calendar_id = e.calendar_id
	            WHERE e.id = $4 AND sb.owner_id = $1)`
	err = pool.QueryRow(ctx, q, owner, d.Entity, d.AID, d.BID).Scan(&a.mapped, &b.mapped, &a.bound, &b.bound)
	return a, b, err
}

// eligible mirrors the app's own auto-resolution ladder: uid and fingerprint
// are exact matches and safe to act on unattended. Fuzzy is a similarity score
// and is only ever included deliberately, and then only at the score the caller
// is willing to treat as identical.
func eligible(d domain.Duplicate, fuzzy bool, minScore float64) bool {
	switch d.Reason {
	case domain.ReasonUID, domain.ReasonFingerprint:
		return true
	case domain.ReasonFuzzy:
		return fuzzy && d.Score >= minScore
	}
	return false
}

// choose returns the action that keeps the better copy, and why.
func choose(a, b side, d domain.Duplicate) (string, string) {
	switch {
	case a.mapped && !b.mapped:
		return dedup.ActionKeepA, "A is linked to Google"
	case b.mapped && !a.mapped:
		return dedup.ActionKeepB, "B is linked to Google"
	case a.bound && !b.bound:
		return dedup.ActionKeepA, "A is in the calendar synced to Google"
	case b.bound && !a.bound:
		return dedup.ActionKeepB, "B is in the calendar synced to Google"
	}
	// Nothing to tell them apart. IDs are UUIDv7 and therefore ordered by
	// creation, so the smaller one is the copy that existed first.
	if d.AID.String() <= d.BID.String() {
		return dedup.ActionKeepA, "both alike, A is the original"
	}
	return dedup.ActionKeepB, "both alike, B is the original"
}

func (a *app) ownerByEmail(ctx context.Context, email string) (domain.ID, error) {
	var id domain.ID
	err := a.withUser(ctx, email, func(_ *auth.Service, u domain.User) error {
		id = u.ID
		return nil
	})
	return id, err
}

func dedupList(fs *flag.FlagSet) runFunc {
	fuzzy := fs.Bool("fuzzy", false, "also include similarity matches, not only exact ones")
	minScore := fs.Float64("min-score", 1, "smallest similarity `score` to treat as identical")
	return func(ctx context.Context, a *app, args []string) error {
		if len(args) != 1 {
			return errUsage
		}
		owner, err := a.ownerByEmail(ctx, args[0])
		if err != nil {
			return err
		}
		svc, pool, done, err := a.dedupService(ctx)
		if err != nil {
			return err
		}
		defer done()
		pairs, err := svc.List(ctx, owner, domain.DupPending)
		if err != nil {
			return err
		}
		out := make([]dedupReport, 0, len(pairs))
		for _, p := range pairs {
			d := p.Duplicate
			if !eligible(d, *fuzzy, *minScore) {
				continue
			}
			am, bm, err := sides(ctx, pool, owner, d)
			if err != nil {
				return err
			}
			action, why := choose(am, bm, d)
			keep, drop := d.AID, d.BID
			if action == dedup.ActionKeepB {
				keep, drop = d.BID, d.AID
			}
			out = append(out, dedupReport{
				ID: d.ID.String(), Entity: d.Entity, Reason: d.Reason, Score: d.Score,
				Keep: keep.String(), Drop: drop.String(), Why: why,
			})
		}
		if a.json {
			return a.printJSON(out)
		}
		if len(out) == 0 {
			a.out.Printf("no pending duplicates\n")
			return nil
		}
		for _, r := range out {
			a.out.Printf("%s  %s/%s  keep %s  drop %s  (%s)\n", r.ID, r.Entity, r.Reason, r.Keep, r.Drop, r.Why)
		}
		a.out.Printf("\n%d pending duplicate(s)\n", len(out))
		return nil
	}
}

func dedupResolve(fs *flag.FlagSet) runFunc {
	fuzzy := fs.Bool("fuzzy", false, "also include similarity matches, not only exact ones")
	minScore := fs.Float64("min-score", 1, "smallest similarity `score` to treat as identical")
	dry := fs.Bool("dry-run", false, "report what would change without touching anything")
	return func(ctx context.Context, a *app, args []string) error {
		if len(args) != 1 {
			return errUsage
		}
		owner, err := a.ownerByEmail(ctx, args[0])
		if err != nil {
			return err
		}
		svc, pool, done, err := a.dedupService(ctx)
		if err != nil {
			return err
		}
		defer done()
		pairs, err := svc.List(ctx, owner, domain.DupPending)
		if err != nil {
			return err
		}
		var resolved, skipped int
		for _, p := range pairs {
			d := p.Duplicate
			if !eligible(d, *fuzzy, *minScore) {
				skipped++
				continue
			}
			am, bm, err := sides(ctx, pool, owner, d)
			if err != nil {
				return err
			}
			action, why := choose(am, bm, d)
			if *dry {
				a.out.Printf("would %s for %s (%s)\n", action, d.ID, why)
				resolved++
				continue
			}
			// Through the service, so the delete is recorded as a change and
			// queued for Google; a raw DELETE would do neither.
			switch err := svc.Resolve(ctx, owner, d.ID, action); {
			case err == nil:
				resolved++
			case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrConflict):
				// Resolving a pair deletes an event and drops the other pairs
				// that referenced it, so a later one can legitimately be gone.
				// Say which, rather than reporting a bare count that hides a
				// real failure among the expected ones.
				a.out.Printf("skipped %s: %v\n", d.ID, err)
				skipped++
			default:
				return fmt.Errorf("resolve %s: %w", d.ID, err)
			}
		}
		verb := "resolved"
		if *dry {
			verb = "would resolve"
		}
		a.out.Printf("%s %d duplicate(s), skipped %d\n", verb, resolved, skipped)
		return nil
	}
}

// dedupScan runs the sweep in-process. Service.Scan only enqueues a job, which
// needs a worker running and gives no way to wait for the result; resolving is
// pointless until a scan has surfaced the pairs to resolve.
func dedupScan(fs *flag.FlagSet) runFunc {
	full := fs.Bool("full", false, "scan the whole calendar, not just the rolling window the nightly sweep uses")
	return func(ctx context.Context, a *app, args []string) error {
		if len(args) != 1 {
			return errUsage
		}
		owner, err := a.ownerByEmail(ctx, args[0])
		if err != nil {
			return err
		}
		svc, _, done, err := a.dedupService(ctx)
		if err != nil {
			return err
		}
		defer done()
		payload, err := json.Marshal(dedup.ScanJob{Owner: owner, Full: *full})
		if err != nil {
			return err
		}
		if err := svc.HandleScan(ctx, jobs.Job{Payload: payload}); err != nil {
			return err
		}
		pairs, err := svc.List(ctx, owner, domain.DupPending)
		if err != nil {
			return err
		}
		a.out.Printf("scan complete, %d pending duplicate(s)\n", len(pairs))
		return nil
	}
}
