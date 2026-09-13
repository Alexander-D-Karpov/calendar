package service

import (
	"errors"
	"slices"
	"testing"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

func fieldNames(t *testing.T, err error) []string {
	t.Helper()
	var ve *domain.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected validation error, got %v", err)
	}
	out := make([]string, len(ve.Fields))
	for i, f := range ve.Fields {
		out[i] = f.Field
	}
	return out
}

func TestApplyCalendar(t *testing.T) {
	c := domain.Calendar{Color: domain.DefaultCalendarColor, Position: -1, DefaultReminders: []int{}}
	err := applyCalendar(&c, domain.CalendarPatch{
		Name:             domain.Some(" Work "),
		Color:            domain.Some("#3B6EA5"),
		Timezone:         domain.Some("Europe/Moscow"),
		DefaultReminders: domain.Some([]int{30, 10, 10}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "Work" || c.Color != "#3b6ea5" || c.Timezone != "Europe/Moscow" || c.Position != -1 || !slices.Equal(c.DefaultReminders, []int{10, 30}) {
		t.Fatalf("calendar = %+v", c)
	}

	if err := applyCalendar(&c, domain.CalendarPatch{Timezone: domain.Opt[string]{Set: true, Null: true}}); err != nil || c.Timezone != "" {
		t.Fatalf("clear timezone = %q %v", c.Timezone, err)
	}

	err = applyCalendar(&c, domain.CalendarPatch{
		Name:             domain.Opt[string]{Set: true, Null: true},
		Color:            domain.Some("red"),
		Timezone:         domain.Some("Mars/Base"),
		Position:         domain.Some(20000),
		DefaultReminders: domain.Some([]int{-1}),
	})
	want := []string{"name", "color", "position", "timezone", "default_reminders"}
	if got := fieldNames(t, err); !slices.Equal(got, want) {
		t.Fatalf("fields = %v", got)
	}

	empty := domain.Calendar{}
	if got := fieldNames(t, applyCalendar(&empty, domain.CalendarPatch{})); !slices.Equal(got, []string{"name"}) {
		t.Fatalf("empty fields = %v", got)
	}
}
