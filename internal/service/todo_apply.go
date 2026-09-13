package service

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/recurrence"
)

func applyTodo(t *domain.Todo, p domain.TodoPatch, isNew bool, now time.Time) error {
	var v domain.ValidationError
	if x, ok := p.Title.Value(&v, "title"); ok {
		t.Title = strings.TrimSpace(x)
	}
	setText(p.Body, &t.Body, false)
	if x, ok := p.Priority.Value(&v, "priority"); ok {
		if x < 0 || x > domain.MaxTodoPriority {
			v.Addf("priority", "must be between 0 and %d", domain.MaxTodoPriority)
		} else {
			t.Priority = x
		}
	}
	if o := p.DueDate; o.Set {
		s := strings.TrimSpace(o.V)
		switch d, ok := parseDate(s); {
		case o.Null || s == "":
			t.DueDate, t.DueTime, t.Duration, t.RRule = nil, nil, 0, ""
		case ok:
			t.DueDate = &d
		default:
			v.Add("due_date", "must be a date like 2026-09-12")
		}
	}
	if o := p.DueTime; o.Set {
		s := strings.TrimSpace(o.V)
		switch m, ok := domain.ParseClock(s); {
		case o.Null || s == "":
			t.DueTime, t.Duration = nil, 0
		case ok:
			t.DueTime = &m
		default:
			v.Add("due_time", "must be a time like 18:00")
		}
	}
	if o := p.DurationMin; o.Set {
		switch {
		case o.Null:
			t.Duration = 0
		case o.V < 1 || o.V > 1440:
			v.Add("duration_min", "must be between 1 and 1440")
		default:
			t.Duration = o.V
		}
	}
	if o := p.Timezone; o.Set {
		tz := strings.TrimSpace(o.V)
		switch {
		case o.Null || tz == "":
			t.TZ = ""
		case domain.ValidTimezone(tz):
			t.TZ = tz
		default:
			v.Add("timezone", "unknown timezone")
		}
	}
	if o := p.RRule; o.Set {
		rule := strings.TrimSpace(o.V)
		if o.Null || rule == "" {
			t.RRule = ""
		} else if norm, err := recurrence.Normalize(rule, t.Zone()); err != nil {
			v.Add("rrule", err.Error())
		} else {
			t.RRule = norm
		}
	}
	if x, ok := p.ShowOnCalendar.Value(&v, "show_on_calendar"); ok {
		t.ShowOnCalendar = x
	} else if isNew && !p.ShowOnCalendar.Set {
		t.ShowOnCalendar = t.DueTime != nil
	}
	if p.Reminders.Set {
		t.Reminders = domain.NormalizeReminders(&v, "reminders", p.Reminders.V)
	}
	if p.Checks.Set {
		switch {
		case !isNew:
			v.Add("checks", "change checklist items with the checks endpoints")
		case len(p.Checks.V) > domain.MaxChecks:
			v.Addf("checks", "must have at most %d items", domain.MaxChecks)
		default:
			t.Checks = newChecks(&v, t.ID, p.Checks.V, now)
		}
	}
	v.Length("title", t.Title, 1, 1000)
	v.Length("body", t.Body, 0, 100000)
	switch {
	case t.DueTime != nil && t.DueDate == nil:
		v.Add("due_time", "requires due_date")
	case t.DueTime != nil && t.TZ == "":
		v.Add("timezone", "is required when due_time is set")
	}
	if t.Duration > 0 && t.DueTime == nil {
		v.Add("duration_min", "requires due_time")
	}
	if t.RRule != "" && t.DueDate == nil {
		v.Add("rrule", "requires due_date")
	}
	return v.Err()
}

func newChecks(v *domain.ValidationError, todo domain.ID, in []domain.CheckInput, now time.Time) []domain.Check {
	out := make([]domain.Check, 0, len(in))
	prev := ""
	for i, c := range in {
		text := strings.TrimSpace(c.Text)
		if n := utf8.RuneCountInString(text); n < 1 || n > 500 {
			v.Addf("checks", "item %d must be 1 to 500 characters", i+1)
			continue
		}
		ch := domain.Check{ID: domain.NewID(), TodoID: todo, Text: text, Done: c.Done, Position: domain.KeyBetween(prev, "")}
		if c.Done {
			at := now
			ch.DoneAt = &at
		}
		prev = ch.Position
		out = append(out, ch)
	}
	return out
}

func applyCheck(c *domain.Check, p domain.CheckPatch, isNew bool, now time.Time) error {
	var v domain.ValidationError
	if x, ok := p.Text.Value(&v, "text"); ok {
		c.Text = strings.TrimSpace(x)
	}
	if x, ok := p.Done.Value(&v, "done"); ok && x != c.Done {
		c.Done = x
		c.DoneAt = nil
		if x {
			at := now
			c.DoneAt = &at
		}
	}
	if !hasField(&v, "text") {
		v.Length("text", c.Text, 1, 500)
	}
	return v.Err()
}
