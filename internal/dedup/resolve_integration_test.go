//go:build integration

package dedup_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/dedup"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/jobs"
	"github.com/Alexander-D-Karpov/calendar/internal/service"
	"github.com/Alexander-D-Karpov/calendar/internal/store/pg"
	"github.com/Alexander-D-Karpov/calendar/internal/testutil/pgtest"
)

// Resolving a pair deletes one event, and that delete drops every duplicate
// record referencing it, including the pair being resolved. Marking the pair
// afterwards then matches no rows, which used to surface as ErrNotFound and
// roll the whole transaction back: the event came back and the pair stayed
// pending, so the same duplicates survived every attempt to resolve them.
func TestResolveDeletesTheDuplicateAndClearsThePair(t *testing.T) {
	ctx := context.Background()
	st := pg.New(pgtest.New(t).Pool)

	u, err := st.CreateUser(ctx, domain.NewUser{
		ID: domain.NewID(), Email: "dedup@example.com", Timezone: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	cals, err := st.ListCalendars(ctx, u.ID)
	if err != nil || len(cals) == 0 {
		t.Fatalf("calendars = %v %v", cals, err)
	}
	cal := cals[0]

	events := service.NewEvents(st, st, st, clock.New(), 5000)
	mk := func() domain.Event {
		out, err := events.Create(ctx, u.ID, domain.EventPatch{
			CalendarID: domain.Some(cal.ID.String()),
			Title:      domain.Some("Appointment"),
			Start:      domain.Some("2026-09-17T09:45:00Z"),
			End:        domain.Some("2026-09-17T10:30:00Z"),
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	keep, drop := mk(), mk()

	svc := dedup.New(dedup.Options{
		Repo: st, Clock: clock.New(), Logger: slog.New(slog.DiscardHandler),
		Enqueue: func(context.Context, string, any, jobs.Options) error { return nil },
	})
	payload, _ := json.Marshal(dedup.ScanJob{Owner: u.ID})
	if err := svc.HandleScan(ctx, jobs.Job{Payload: payload}); err != nil {
		t.Fatal(err)
	}
	pairs, err := svc.List(ctx, u.ID, domain.DupPending)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) == 0 {
		t.Fatal("scan found no duplicates for two identical events")
	}

	d := pairs[0].Duplicate
	action := dedup.ActionKeepA
	if d.AID == drop.ID {
		action = dedup.ActionKeepB
	}
	if d.AID != keep.ID && d.BID != keep.ID {
		t.Fatalf("pair %s does not reference the events under test", d.ID)
	}
	if err := svc.Resolve(ctx, u.ID, d.ID, action); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	left, err := svc.List(ctx, u.ID, domain.DupPending)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range left {
		if p.Duplicate.ID == d.ID {
			t.Fatal("the pair is still pending after a successful resolve")
		}
	}
	var live int
	const q = `SELECT count(*) FROM events WHERE owner_id = $1 AND deleted_at IS NULL`
	if err := st.Pool().QueryRow(ctx, q, u.ID).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 1 {
		t.Fatalf("events = %d, want 1: the duplicate was not removed", live)
	}
}

// The nightly sweep only looks at a rolling window, so duplicates further out
// than that stay invisible: changing the import target calendar left one
// account with 133 duplicated events that every scan reported as clean.
func TestFullScanFindsDuplicatesOutsideTheRollingWindow(t *testing.T) {
	ctx := context.Background()
	st := pg.New(pgtest.New(t).Pool)

	u, err := st.CreateUser(ctx, domain.NewUser{
		ID: domain.NewID(), Email: "window@example.com", Timezone: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	cals, err := st.ListCalendars(ctx, u.ID)
	if err != nil || len(cals) == 0 {
		t.Fatalf("calendars = %v %v", cals, err)
	}

	// Two identical events well beyond the window the sweep covers.
	far := time.Now().UTC().AddDate(3, 0, 0).Format("2006-01-02")
	events := service.NewEvents(st, st, st, clock.New(), 5000)
	for range 2 {
		if _, err := events.Create(ctx, u.ID, domain.EventPatch{
			CalendarID: domain.Some(cals[0].ID.String()),
			Title:      domain.Some("Far future"),
			Start:      domain.Some(far + "T09:00:00Z"),
			End:        domain.Some(far + "T10:00:00Z"),
		}); err != nil {
			t.Fatal(err)
		}
	}

	svc := dedup.New(dedup.Options{
		Repo: st, Clock: clock.New(), Logger: slog.New(slog.DiscardHandler),
		Enqueue: func(context.Context, string, any, jobs.Options) error { return nil },
	})
	scan := func(full bool) int {
		t.Helper()
		payload, _ := json.Marshal(dedup.ScanJob{Owner: u.ID, Full: full})
		if err := svc.HandleScan(ctx, jobs.Job{Payload: payload}); err != nil {
			t.Fatal(err)
		}
		pairs, err := svc.List(ctx, u.ID, domain.DupPending)
		if err != nil {
			t.Fatal(err)
		}
		return len(pairs)
	}

	if n := scan(false); n != 0 {
		t.Fatalf("windowed scan found %d pairs, expected it to miss them", n)
	}
	if n := scan(true); n == 0 {
		t.Fatal("full scan found nothing: duplicates outside the window stay hidden")
	}
}
