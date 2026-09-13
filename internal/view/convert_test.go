package view

import (
	"slices"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

func TestFromOccurrences(t *testing.T) {
	work := domain.Calendar{ID: domain.NewID(), Name: "Work", Color: "#3b6ea5"}
	hidden := domain.Calendar{ID: domain.NewID(), Name: "Old", Color: "#aa3355", Hidden: true}
	start := time.Date(2026, 9, 10, 7, 0, 0, 0, time.UTC)
	m := domain.Event{ID: domain.NewID(), CalendarID: work.ID, Start: start, End: start.Add(time.Hour), TZ: "UTC", RRule: "FREQ=DAILY", Body: "**Agenda**"}
	items := FromOccurrences([]domain.Occurrence{{Event: m, Instance: &start}}, []domain.Calendar{work, hidden})
	if len(items) != 1 || items[0].Color != "3b6ea5" || items[0].Instance != "2026-09-10T07:00:00Z" || items[0].Excerpt != "Agenda" {
		t.Fatalf("items = %+v", items)
	}
	if got := Colors(items, []domain.Calendar{work, hidden}); !slices.Equal(got, []string{"3b6ea5", "aa3355"}) {
		t.Fatalf("colors = %v", got)
	}
}

func TestFromTodos(t *testing.T) {
	list := domain.TodoList{ID: domain.NewID(), Name: "Inbox", Color: "#5b7c5a"}
	due, tm := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC), 18*60
	timed := domain.Todo{ID: domain.NewID(), ListID: list.ID, Title: "GPU", DueDate: &due, DueTime: &tm, TZ: "UTC", Checks: []domain.Check{{Done: true}, {}}}
	dated := domain.Todo{ID: domain.NewID(), ListID: list.ID, Title: "Pay", DueDate: &due}
	undated := domain.Todo{ID: domain.NewID(), ListID: list.ID, Title: "Someday"}
	items := FromTodos([]domain.Todo{timed, dated, undated}, []domain.TodoList{list})
	if len(items) != 2 {
		t.Fatalf("items = %d", len(items))
	}
	a, b := items[0], items[1]
	if !a.Todo || a.AllDay || a.Progress() != "1/2" || a.Href() != "/todos/"+timed.ID.String() || a.Color != "5b7c5a" || a.End.Sub(a.Start) != 30*time.Minute {
		t.Fatalf("timed = %+v", a)
	}
	if !b.AllDay || b.Progress() != "" {
		t.Fatalf("dated = %+v", b)
	}
}

func TestRedact(t *testing.T) {
	items := []Item{
		{ID: "a", Title: "Standup", Location: "Office", Excerpt: "notes", CalendarName: "Work"},
		{ID: "p", Title: "Doctor", Private: true},
		{ID: "t", Title: "Pay", Todo: true, ChecksTotal: 2},
	}
	busy := Redact(items, domain.DetailBusy)
	if len(busy) != 2 || busy[0].Title != "Busy" || busy[0].Location != "" || busy[0].Href() != "" || busy[1].ToggleHref() != "" || busy[1].ChecksTotal != 0 {
		t.Fatalf("busy = %+v", busy)
	}
	titles := Redact(items, domain.DetailTitles)
	if titles[0].Title != "Standup" || titles[0].Location != "" || titles[0].Excerpt != "" {
		t.Fatalf("titles = %+v", titles)
	}
	full := Redact(items, domain.DetailFull)
	if full[0].Location != "Office" || !full[0].Static {
		t.Fatalf("full = %+v", full)
	}
}
