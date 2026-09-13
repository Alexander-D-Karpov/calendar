//go:build integration

package search_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/search"
	"github.com/Alexander-D-Karpov/calendar/internal/service"
	"github.com/Alexander-D-Karpov/calendar/internal/store/pg"
	"github.com/Alexander-D-Karpov/calendar/internal/testutil/pgtest"
)

func TestSearch(t *testing.T) {
	ctx := context.Background()
	st := pg.New(pgtest.New(t).Pool)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(now)
	u, err := st.CreateUser(ctx, domain.NewUser{ID: domain.NewID(), Email: "search@example.com", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	events := service.NewEvents(st, st, st, clk, 5000)
	todos := service.NewTodos(st, st, clk)
	lists := service.NewTodoLists(st)
	cals, _ := st.ListCalendars(ctx, u.ID)

	mk := func(title, body, loc, start string, rrule string) domain.ID {
		t.Helper()
		p := domain.EventPatch{
			Title: domain.Some(title), Body: domain.Some(body), Location: domain.Some(loc),
			Start: domain.Some(start), CalendarID: domain.Some(cals[0].ID.String()),
		}
		if rrule != "" {
			p.RRule = domain.Some(rrule)
		}
		e, err := events.Create(ctx, u.ID, p)
		if err != nil {
			t.Fatal(err)
		}
		return e.ID
	}
	standup := mk("Standup", "Agenda and **notes**", "Office", "2026-09-11T10:00:00Z", "FREQ=WEEKLY;BYDAY=MO")
	mk("Café meeting", "", "", "2026-09-12T10:00:00Z", "")
	mk("Retro", "", "", "2026-09-13T10:00:00Z", "")

	work, err := lists.Create(ctx, u.ID, domain.TodoListPatch{Name: domain.Some("Work")})
	if err != nil {
		t.Fatal(err)
	}
	gpu, err := todos.Create(ctx, u.ID, domain.TodoPatch{
		Title: domain.Some("Assemble GPU node"), Body: domain.Some("Check PSU rails"), Priority: domain.Some(3),
		DueDate: domain.Some("2026-09-05"), ListID: domain.Some(work.ID.String()),
		Checks: domain.Some([]domain.CheckInput{{Text: "Riser cables"}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := todos.Complete(ctx, u.ID, gpu.ID, ""); err != nil {
		t.Fatal(err)
	}
	pending, err := todos.Create(ctx, u.ID, domain.TodoPatch{
		Title: domain.Some("Order fans"), DueDate: domain.Some("2026-09-01"), ListID: domain.Some(work.ID.String()),
	})
	if err != nil {
		t.Fatal(err)
	}

	svc := search.NewService(st, clk)
	find := func(raw string) []search.Hit {
		t.Helper()
		res, err := svc.Search(ctx, u.ID, search.Request{Raw: raw})
		if err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
		return res.Hits
	}
	ids := func(hits []search.Hit) []string {
		out := make([]string, len(hits))
		for i, h := range hits {
			out[i] = h.ID.String()
		}
		slices.Sort(out)
		return out
	}

	if hits := find("standup"); len(hits) != 1 || hits[0].ID != standup || hits[0].Snippet != "Agenda and notes" {
		t.Fatalf("title match = %+v", hits)
	}
	if hits := find("agenda"); len(hits) != 1 {
		t.Fatalf("body match = %+v", hits)
	}
	if hits := find("cafe"); len(hits) != 1 {
		t.Fatalf("unaccent match = %+v", hits)
	}
	if hits := find("stand"); len(hits) != 1 {
		t.Fatalf("prefix match = %+v", hits)
	}
	if hits := find("riser"); len(hits) != 1 || hits[0].ID != gpu.ID {
		t.Fatalf("checklist match = %+v", hits)
	}
	if hits := find("standup -agenda"); len(hits) != 0 {
		t.Fatalf("exclusion = %+v", hits)
	}
	if hits := find("type:todo is:open"); len(hits) != 1 || hits[0].ID != pending.ID {
		t.Fatalf("is:open = %+v", hits)
	}
	if hits := find("is:overdue"); len(hits) != 1 || hits[0].ID != pending.ID {
		t.Fatalf("is:overdue = %+v", hits)
	}
	if hits := find("is:done"); len(hits) != 1 || hits[0].ID != gpu.ID {
		t.Fatalf("is:done = %+v", hits)
	}
	if hits := find("in:Work type:todo"); len(ids(hits)) != 2 {
		t.Fatalf("in: = %+v", hits)
	}
	if hits := find("is:recurring"); len(hits) != 1 || hits[0].ID != standup {
		t.Fatalf("is:recurring = %+v", hits)
	}
	if hits := find("has:checks"); len(hits) != 1 || hits[0].ID != gpu.ID {
		t.Fatalf("has:checks = %+v", hits)
	}
	if hits := find("type:todo priority:>=3"); len(hits) != 1 || hits[0].ID != gpu.ID {
		t.Fatalf("priority = %+v", hits)
	}
	if hits := find("type:event after:2026-09-12"); len(hits) != 2 {
		t.Fatalf("after = %+v", hits)
	}

	res, err := svc.Search(ctx, u.ID, search.Request{Raw: "type:event", Limit: 2})
	if err != nil || len(res.Hits) != 2 || !res.More {
		t.Fatalf("paging = %d more=%v %v", len(res.Hits), res.More, err)
	}
	next, err := svc.Search(ctx, u.ID, search.Request{Raw: "type:event", Limit: 2, Offset: res.Next()})
	if err != nil || len(next.Hits) != 1 || next.More {
		t.Fatalf("second page = %d more=%v %v", len(next.Hits), next.More, err)
	}

	if err := todos.Delete(ctx, u.ID, pending.ID, ""); err != nil {
		t.Fatal(err)
	}
	if hits := find("fans"); len(hits) != 0 {
		t.Fatalf("deleted todos must not appear: %+v", hits)
	}

	other, err := st.CreateUser(ctx, domain.NewUser{ID: domain.NewID(), Email: "other@example.com", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	res, err = svc.Search(ctx, other.ID, search.Request{Raw: "standup"})
	if err != nil || len(res.Hits) != 0 {
		t.Fatalf("another account must see nothing: %+v %v", res.Hits, err)
	}
}
