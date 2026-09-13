package service

import (
	"slices"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

func TestApplyTodo(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	td := domain.Todo{ID: domain.NewID(), TZ: "Europe/Moscow"}
	err := applyTodo(&td, domain.TodoPatch{
		Title:       domain.Some(" GPU node "),
		DueDate:     domain.Some("2026-09-12"),
		DueTime:     domain.Some("18:00"),
		DurationMin: domain.Some(90),
		Checks:      domain.Some([]domain.CheckInput{{Text: "Risers", Done: true}, {Text: "Paste"}}),
	}, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if td.Title != "GPU node" || *td.DueTime != 18*60 || !td.ShowOnCalendar || len(td.Checks) != 2 || td.Checks[0].DoneAt == nil || td.Checks[0].Position >= td.Checks[1].Position {
		t.Fatalf("todo = %+v", td)
	}
	start, end, allDay, ok := td.Span()
	if !ok || allDay || !start.Equal(time.Date(2026, 9, 12, 15, 0, 0, 0, time.UTC)) || end.Sub(start) != 90*time.Minute {
		t.Fatalf("span = %v %v %v", start, end, allDay)
	}

	if err := applyTodo(&td, domain.TodoPatch{DueDate: domain.Opt[string]{Set: true, Null: true}}, false, now); err != nil {
		t.Fatal(err)
	}
	if td.DueDate != nil || td.DueTime != nil || td.Duration != 0 {
		t.Fatalf("cleared = %+v", td)
	}

	err = applyTodo(&td, domain.TodoPatch{
		DueTime:  domain.Some("25:00"),
		Priority: domain.Some(5),
		RRule:    domain.Some("FREQ=DAILY"),
		Checks:   domain.Some([]domain.CheckInput{{Text: "x"}}),
	}, false, now)
	got := fieldNames(t, err)
	for _, f := range []string{"due_time", "priority", "rrule", "checks"} {
		if !slices.Contains(got, f) {
			t.Errorf("missing %s in %v", f, got)
		}
	}
	if got := fieldNames(t, applyTodo(&domain.Todo{}, domain.TodoPatch{}, true, now)); !slices.Equal(got, []string{"title"}) {
		t.Fatalf("empty = %v", got)
	}
}
