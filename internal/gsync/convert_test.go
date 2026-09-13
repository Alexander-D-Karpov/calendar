package gsync

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/google"
)

func TestEventRoundTrip(t *testing.T) {
	re := google.Event{
		ID: "a", Summary: "Standup", Description: "<b>Agenda</b>", Editable: true,
		Start:      google.When{DateTime: "2026-09-11T10:00:00+03:00", TimeZone: "Europe/Moscow"},
		End:        google.When{DateTime: "2026-09-11T10:15:00+03:00", TimeZone: "Europe/Moscow"},
		Recurrence: []string{"RRULE:FREQ=WEEKLY;BYDAY=MO", "EXDATE;TZID=Europe/Moscow:20260914T100000"},
		Reminders:  []int{30, 10, 10, -5},
	}
	ev, ok := eventFromRemote(re, domain.Event{}, []string{re.Start.TimeZone}, nil)
	if !ok || ev.TZ != "Europe/Moscow" || !ev.Start.Equal(time.Date(2026, 9, 11, 7, 0, 0, 0, time.UTC)) || ev.Body != "**Agenda**" || ev.ReadOnly {
		t.Fatalf("event = %+v", ev)
	}
	if !strings.Contains(ev.RRule, "FREQ=WEEKLY") || len(ev.ExDate) != 1 || !ev.ExDate[0].Equal(time.Date(2026, 9, 14, 7, 0, 0, 0, time.UTC)) || !slices.Equal(ev.Reminders, []int{10, 30}) {
		t.Fatalf("recurrence = %q %v %v", ev.RRule, ev.ExDate, ev.Reminders)
	}
	back := toRemoteEvent(ev, false)
	if back.Start.DateTime != "2026-09-11T10:00:00+03:00" || back.Start.TimeZone != "Europe/Moscow" || !back.SendRecurrence ||
		!slices.Contains(back.Recurrence, "EXDATE;TZID=Europe/Moscow:20260914T100000") {
		t.Fatalf("back = %+v", back)
	}

	lossy := re
	lossy.Recurrence = []string{"RRULE:FREQ=HOURLY"}
	if ev, _ := eventFromRemote(lossy, domain.Event{}, nil, nil); ev.RRule != "" || !ev.ReadOnly {
		t.Fatalf("lossy = %+v", ev)
	}

	allDay := google.Event{Editable: true, Start: google.When{Date: "2026-09-20"}, End: google.When{Date: "2026-09-22"}, UseDefaultReminders: true}
	ev, ok = eventFromRemote(allDay, domain.Event{}, nil, []int{15})
	if !ok || !ev.AllDay || ev.End.Sub(ev.Start) != 48*time.Hour || ev.TZ != "UTC" || !slices.Equal(ev.Reminders, []int{15}) {
		t.Fatalf("all day = %+v", ev)
	}
	if b := toRemoteEvent(ev, true); b.Start.Date != "2026-09-20" || b.End.Date != "2026-09-22" || b.SendRecurrence {
		t.Fatalf("all day back = %+v", b)
	}
}

func TestTodoRoundTrip(t *testing.T) {
	due := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	done := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	td := domain.Todo{
		Title: "GPU", Body: "PSU", DueDate: &due, Status: domain.TodoCompleted, CompletedAt: &done,
		Checks: []domain.Check{{Text: "Risers", Done: true}, {Text: "Paste"}},
	}
	rt := taskFromTodo(td, "p1")
	if rt.Notes != "PSU\n\n---\n[x] Risers\n[ ] Paste" || rt.Due != "2026-09-12" || rt.Status != google.TaskCompleted || rt.Parent != "p1" || !rt.Completed.Equal(done) {
		t.Fatalf("task = %+v", rt)
	}
	var back domain.Todo
	checks := fillTodo(&back, rt, nil, done)
	if back.Title != "GPU" || back.Body != "PSU" || !back.Done() || !sameChecks(td.Checks, checks) || !back.DueDate.Equal(due) {
		t.Fatalf("back = %+v %v", back, checks)
	}

	tm := 18 * 60
	timed := domain.Todo{DueDate: &due, DueTime: &tm, TZ: "UTC"}
	m := domain.SyncMapping{RemoteDueDate: &due}
	fillTodo(&timed, google.Task{Title: "x", Due: "2026-09-12"}, &m, done)
	if timed.DueTime == nil || !timed.DueDate.Equal(due) {
		t.Fatal("an unchanged remote date must keep the local time")
	}
	fillTodo(&timed, google.Task{Title: "x", Due: "2026-09-13"}, &m, done)
	if timed.DueTime == nil || timed.DueDate.Day() != 13 {
		t.Fatal("a moved remote date must keep the time on the new day")
	}
	fillTodo(&timed, google.Task{}, &m, done)
	if timed.DueDate != nil || timed.DueTime != nil || timed.Title != noTitle {
		t.Fatalf("cleared = %+v", timed)
	}
}
