package quickadd

import (
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

func opts(t *testing.T) Options {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	return Options{Now: time.Date(2026, 9, 12, 14, 30, 0, 0, loc), Loc: loc, WeekStart: time.Monday}
}

func TestParse(t *testing.T) {
	cases := []struct {
		in       string
		title    string
		date     string
		clock    string
		end      string
		duration int
		priority int
		list     string
	}{
		{in: "buy fans", title: "buy fans"},
		{in: "tomorrow 18:00 buy fans", title: "buy fans", date: "2026-09-13", clock: "18:00"},
		{in: "standup 9:30am every desk", title: "standup every desk", clock: "09:30"},
		{in: "gym 18:00-19:30", title: "gym", clock: "18:00", end: "19:30"},
		{in: "call mike for 45m at 11:00", title: "call mike", clock: "11:00", duration: 45},
		{in: "pay rent in 3 days !!", title: "pay rent", date: "2026-09-15", priority: 2},
		{in: "dentist 24 sep at 2pm", title: "dentist", date: "2026-09-24", clock: "14:00"},
		{in: "review pr #work !", title: "review pr", list: "work", priority: 1},
		{in: "retro next monday", title: "retro", date: "2026-09-21"},
		{in: "retro monday", title: "retro", date: "2026-09-14"},
		{in: "ship 2026-12-01", title: "ship", date: "2026-12-01"},
		{in: "12.5 kg plates", title: "12.5 kg plates"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := Parse(c.in, opts(t))
			if got.Title != c.title {
				t.Errorf("title = %q, want %q", got.Title, c.title)
			}
			if date(got) != c.date {
				t.Errorf("date = %q, want %q", date(got), c.date)
			}
			if clock(got.Time) != c.clock {
				t.Errorf("time = %q, want %q", clock(got.Time), c.clock)
			}
			if clock(got.End) != c.end {
				t.Errorf("end = %q, want %q", clock(got.End), c.end)
			}
			if got.Duration != c.duration || got.Priority != c.priority || got.List != c.list {
				t.Errorf("got %+v", got)
			}
		})
	}
}

func date(r Result) string {
	if r.Date == nil {
		return ""
	}
	return r.Date.Format(domain.DateLayout)
}

func clock(t *domain.TimeOfDay) string {
	if t == nil {
		return ""
	}
	return t.String()
}

// Patterns that anchor on a surrounding space must not swallow it, or the next
// token starts inside an existing cut and is dropped without a trace.
func TestAdjacentTokens(t *testing.T) {
	cases := map[string]struct {
		title, list string
		prio        int
	}{
		"pay rent !! #home": {"pay rent", "home", 2},
		"pay rent #home !!": {"pay rent", "home", 2},
		"pay rent ! #a-b_1": {"pay rent", "a-b_1", 1},
		"!!! #work ship it": {"ship it", "work", 3},
	}
	for in, want := range cases {
		got := Parse(in, opts(t))
		if got.Title != want.title || got.List != want.list || got.Priority != want.prio {
			t.Errorf("Parse(%q) = title %q list %q priority %d, want %q %q %d",
				in, got.Title, got.List, got.Priority, want.title, want.list, want.prio)
		}
	}
}
