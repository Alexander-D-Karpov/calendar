package todos

import (
	"bytes"
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/store"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

var today = time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)

func date(m time.Month, d int) *time.Time {
	t := time.Date(2026, m, d, 0, 0, 0, 0, time.UTC)
	return &t
}

func TestTodoPatch(t *testing.T) {
	vals := url.Values{
		"form": {"full"}, "title": {" GPU "}, "list_id": {"x"}, "due_date": {"2026-09-12"}, "due_time": {"18:00"},
		"duration": {""}, "timezone": {"Europe/Moscow"}, "repeat": {"weekly"}, "priority": {"2"}, "reminders": {"30"},
		"show": {"auto"}, "body": {"notes\n"},
	}
	p, err := todoPatch(web.NewForm(vals), true)
	if err != nil {
		t.Fatal(err)
	}
	if p.Title.V != "GPU" || !p.ListID.Set || !p.DurationMin.Null || p.RRule.V != "FREQ=WEEKLY" || !p.ShowOnCalendar.V || p.Priority.V != 2 || p.Body.V != "notes" {
		t.Fatalf("patch = %+v", p)
	}
	if p, _ := todoPatch(web.NewForm(vals), false); p.ListID.Set {
		t.Fatal("list must be ignored when not allowed")
	}
	q, err := todoPatch(web.NewForm(url.Values{"title": {"x"}, "due_date": {""}}), true)
	if err != nil || q.DueTime.Set || q.ShowOnCalendar.Set || q.Timezone.Set || !q.DueDate.Set {
		t.Fatalf("quick = %+v %v", q, err)
	}
	_, err = todoPatch(web.NewForm(url.Values{"priority": {"x"}, "duration": {"abc"}, "reminders": {"a"}}), true)
	var ve *domain.ValidationError
	if !errorsAs(err, &ve) || len(ve.Fields) != 3 {
		t.Fatalf("err = %v", err)
	}
	back := formValues(domain.Todo{Title: "GPU", DueDate: date(9, 12), ShowOnCalendar: true}, "UTC")
	if back.Get("show") != "yes" || back.Get("due_date") != "2026-09-12" || back.Get("repeat") != "none" {
		t.Fatalf("values = %v", back)
	}
}

func errorsAs(err error, target **domain.ValidationError) bool {
	return errors.As(err, target)
}

func TestRowsAndLabels(t *testing.T) {
	parent := domain.Todo{ID: domain.NewID(), Title: "P", DueDate: date(9, 12)}
	child := domain.Todo{ID: domain.NewID(), ParentID: &parent.ID, Title: "C"}
	other := domain.NewID()
	orphan := domain.Todo{ID: domain.NewID(), ParentID: &other, Title: "O"}
	rows := buildRows([]domain.Todo{child, parent, orphan}, rowContext{today: today})
	if len(rows) != 2 || rows[0].Todo.ID != parent.ID || len(rows[0].Subtasks) != 1 || rows[1].Todo.ID != orphan.ID {
		t.Fatalf("rows = %+v", rows)
	}
	tm := 18 * 60
	cases := map[string]domain.Todo{
		"Today":                   {DueDate: date(9, 10)},
		"Tomorrow":                {DueDate: date(9, 11)},
		"Yesterday":               {DueDate: date(9, 9)},
		"Monday":                  {DueDate: date(9, 14)},
		"Tue 1 Dec":               {DueDate: date(12, 1)},
		"Today 18:00, 1 h 30 min": {DueDate: date(9, 10), DueTime: &tm, Duration: 90},
	}
	for want, td := range cases {
		if got := dueLabel(td, today, true); got != want {
			t.Errorf("dueLabel = %q, want %q", got, want)
		}
	}
	if !overdue(domain.Todo{DueDate: date(9, 9)}, today) || overdue(domain.Todo{DueDate: date(9, 9), Status: domain.TodoCompleted}, today) {
		t.Fatal("overdue mismatch")
	}
	f, iv := selection(today, nil, "upcoming")
	if iv.Heading != "Upcoming" || !f.DueFrom.Equal(*date(9, 11)) || !f.DueTo.Equal(*date(9, 25)) || f.Status != domain.TodoOpen {
		t.Fatalf("upcoming = %+v %+v", f, iv)
	}
	l := domain.TodoList{ID: domain.NewID(), Name: "Work"}
	f, iv = selection(today, &l, "")
	if iv.QuickList != l.ID.String() || f.Order != store.OrderByPosition || !slices.Equal(f.Lists, []domain.ID{l.ID}) {
		t.Fatalf("list = %+v %+v", f, iv)
	}
}

func TestPagesRender(t *testing.T) {
	r, err := web.EmbeddedRenderer()
	if err != nil {
		t.Fatal(err)
	}
	user := domain.User{ID: domain.NewID(), Email: "sasha@akarpov.ru"}
	list := domain.TodoList{ID: domain.NewID(), Name: "Inbox", Color: "#5b7c5a", IsDefault: true}
	now := time.Now()
	td := domain.Todo{
		ID: domain.NewID(), ListID: list.ID, Title: "Assemble GPU node", Body: "**PSU**", DueDate: date(9, 12), Priority: 3,
		Checks: []domain.Check{{ID: domain.NewID(), Text: "Risers", Done: true, DoneAt: &now}}, Version: 1,
	}
	ctx := rowContext{lists: listMap([]domain.TodoList{list}), today: today, clock24: true, csrf: "tok", back: "/todos"}
	pages := map[string]any{
		"todos": indexView{Heading: "Inbox", Rows: buildRows([]domain.Todo{td}, ctx), Colors: []string{"5b7c5a"},
			Lists: []link{{Label: "Inbox", Href: "/todos?list=x", Hex: "5b7c5a", Current: true}}, QuickList: list.ID.String(), Back: "/todos"},
		"todo": detailView{Todo: td, List: list, Due: "Saturday", Priority: "High", Progress: "1/1", Self: "/todos/" + td.ID.String(),
			Back: "/todos", Hex: "5b7c5a", Colors: []string{"5b7c5a"}},
		"todo_edit": formView{Action: "/todos", Cancel: "/todos", Lists: []domain.TodoList{list}, AllowList: true,
			Repeats: web.RepeatOptions, Priorities: priorityOptions, Shows: showOptions},
	}
	for name, data := range pages {
		p := &web.Page{Title: name, Nav: "todos", User: &user, CSRF: "tok", Form: web.NewForm(formValues(td, "UTC")), Data: data, Footer: &web.Footer{AppName: "Calendar"}}
		var buf bytes.Buffer
		if err := r.Execute(&buf, name, p); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out := buf.String()
		if !strings.Contains(out, "Assemble GPU node") || strings.Contains(out, "style=") {
			t.Errorf("%s: content or inline style mismatch", name)
		}
		if name != "todo_edit" && !strings.Contains(out, `action="/todos/`+td.ID.String()+`/toggle"`) {
			t.Errorf("%s: missing toggle form", name)
		}
	}
}

func TestMovePatch(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}

	timed, err := movePatch(web.NewForm(url.Values{
		"start": {"2026-09-12T14:30"}, "end": {"2026-09-12T15:15"},
	}), loc)
	if err != nil {
		t.Fatal(err)
	}
	if timed.DueDate.V != "2026-09-12" || timed.DueTime.V != "14:30" {
		t.Errorf("timed = %+v", timed)
	}
	if timed.DurationMin.V != 45 {
		t.Errorf("duration = %d, want 45", timed.DurationMin.V)
	}
	if timed.Timezone.V != "Europe/Moscow" || !timed.ShowOnCalendar.V {
		t.Errorf("a timed drop must pin the zone and stay on the calendar: %+v", timed)
	}

	// Dropping onto the all-day lane sends a date alone and must clear the time,
	// or the todo would keep a stale hour it no longer shows.
	allDay, err := movePatch(web.NewForm(url.Values{"date": {"2026-09-12"}}), loc)
	if err != nil {
		t.Fatal(err)
	}
	if allDay.DueDate.V != "2026-09-12" {
		t.Errorf("date = %q", allDay.DueDate.V)
	}
	if !allDay.DueTime.Null || !allDay.DurationMin.Null {
		t.Errorf("an all-day drop must clear the time and duration: %+v", allDay)
	}

	// An end at or before the start says nothing about duration; leave it alone.
	back, err := movePatch(web.NewForm(url.Values{
		"start": {"2026-09-12T14:30"}, "end": {"2026-09-12T14:30"},
	}), loc)
	if err != nil {
		t.Fatal(err)
	}
	if back.DurationMin.Set {
		t.Errorf("a zero span must not set a duration: %+v", back.DurationMin)
	}

	for _, bad := range []url.Values{{}, {"start": {"nonsense"}}, {"date": {"12/09/2026"}}} {
		if _, err := movePatch(web.NewForm(bad), loc); err == nil {
			t.Errorf("movePatch(%v) accepted bad input", bad)
		}
	}
}
