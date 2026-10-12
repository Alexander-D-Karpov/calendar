//go:build integration

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/clock"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/service"
	"github.com/Alexander-D-Karpov/calendar/internal/store/pg"
	"github.com/Alexander-D-Karpov/calendar/internal/testutil/pgtest"
)

func TestDisplay(t *testing.T) {
	ctx := context.Background()
	st := pg.New(pgtest.New(t).Pool)
	clk := clock.NewFake(time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC))
	u, err := st.CreateUser(ctx, domain.NewUser{ID: domain.NewID(), Email: "display@example.com", Timezone: "Europe/Moscow"})
	if err != nil {
		t.Fatal(err)
	}
	d := Deps{
		Settings:  service.NewSettings(st),
		Calendars: service.NewCalendars(st),
		Events:    service.NewEvents(st, st, st, clk, 5000),
		Todos:     service.NewTodos(st, st, clk),
		Clock:     clk,
	}
	ev := func(p domain.EventPatch) {
		t.Helper()
		if _, err := d.Events.Create(ctx, u.ID, p); err != nil {
			t.Fatal(err)
		}
	}
	ev(domain.EventPatch{Title: domain.Some("Созвон с очень длинным названием, которое точно не влезет на маленький экран устройства"), Location: domain.Some("Zoom"), Start: domain.Some("2026-10-12T15:00:00+03:00"), End: domain.Some("2026-10-12T16:00:00+03:00")})
	ev(domain.EventPatch{Title: domain.Some("Trip"), AllDay: domain.Some(true), Start: domain.Some("2026-10-13"), End: domain.Some("2026-10-14")})
	ev(domain.EventPatch{Title: domain.Some("Later"), Start: domain.Some("2026-10-14T10:00:00+03:00")})

	td := func(p domain.TodoPatch) domain.Todo {
		t.Helper()
		x, err := d.Todos.Create(ctx, u.ID, p)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	bills := td(domain.TodoPatch{Title: domain.Some("Pay bills"), Priority: domain.Some(2), DueDate: domain.Some("2026-10-09"),
		Checks: domain.Some([]domain.CheckInput{{Text: "a", Done: true}, {Text: "b"}, {Text: "c"}})})
	td(domain.TodoPatch{Title: domain.Some("Sub"), DueDate: domain.Some("2026-10-12"), ParentID: domain.Some(bills.ID.String())})
	td(domain.TodoPatch{Title: domain.Some("Ancient"), DueDate: domain.Some("2026-09-20")})
	td(domain.TodoPatch{Title: domain.Some("Far"), DueDate: domain.Some("2026-10-14")})
	td(domain.TodoPatch{Title: domain.Some("Call"), DueDate: domain.Some("2026-10-13"), DueTime: domain.Some("18:30")})

	get := func(query, inm string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, Prefix+"/display"+query, nil)
		r = r.WithContext(auth.WithPrincipal(r.Context(), &auth.Principal{UserID: u.ID}))
		if inm != "" {
			r.Header.Set("If-None-Match", inm)
		}
		w := httptest.NewRecorder()
		if err := getDisplay(d)(w, r); err != nil {
			t.Fatal(err)
		}
		return w
	}

	w := get("", "")
	var out displayJSON
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.TZOffset != 3*3600 || out.Now != clk.Now().Unix() || out.Weather != nil {
		t.Fatalf("header fields: %+v", out)
	}
	if len(out.Events) != 2 {
		t.Fatalf("events = %+v", out.Events)
	}
	if e := out.Events[0]; e.Start != time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC).Unix() || e.Place != "Zoom" || len([]rune(e.Title)) != displayTitleLen || !strings.HasSuffix(e.Title, "…") || e.Color == "" {
		t.Fatalf("timed event = %+v", e)
	}
	if e := out.Events[1]; !e.AllDay || e.Start != time.Date(2026, 10, 12, 21, 0, 0, 0, time.UTC).Unix() {
		t.Fatalf("all-day event = %+v", e)
	}
	if len(out.Todos) != 2 {
		t.Fatalf("todos = %+v", out.Todos)
	}
	if x := out.Todos[0]; x.Title != "Pay bills" || x.Due != "2026-10-09" || x.Done != 1 || x.Total != 3 || x.Priority != 2 || x.Time != nil {
		t.Fatalf("overdue todo = %+v", x)
	}
	if x := out.Todos[1]; x.Title != "Call" || x.Time == nil || *x.Time != 18*60+30 {
		t.Fatalf("timed todo = %+v", x)
	}
	tag := w.Header().Get("ETag")
	if tag != `"`+out.ETag+`"` {
		t.Fatalf("ETag header %q, body %q", tag, out.ETag)
	}

	clk.Advance(3 * time.Minute)
	if w := get("", tag); w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Fatalf("unchanged poll: %d %q", w.Code, w.Body.String())
	}
	if w := get("?days=3", tag); w.Code != http.StatusOK {
		t.Fatalf("wider window should change the tag, got %d", w.Code)
	}
	for _, q := range []string{"?days=0", "?days=8", "?lat=55", "?lat=91&lon=0"} {
		r := httptest.NewRequest(http.MethodGet, Prefix+"/display"+q, nil)
		r = r.WithContext(auth.WithPrincipal(r.Context(), &auth.Principal{UserID: u.ID}))
		if err := getDisplay(d)(httptest.NewRecorder(), r); err == nil {
			t.Errorf("%s: want a validation error", q)
		}
	}
}
