package calendar

import (
	"errors"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

func TestEventPatchRoundTrip(t *testing.T) {
	start := time.Date(2026, 9, 10, 7, 0, 0, 0, time.UTC)
	e := domain.Event{
		ID: domain.NewID(), CalendarID: domain.NewID(), Title: "Standup", Start: start, End: start.Add(15 * time.Minute),
		TZ: "Europe/Moscow", RRule: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR", Status: domain.StatusConfirmed,
		Visibility: domain.VisibilityPrivate, Transparency: domain.TransparencyOpaque, Reminders: []int{10, 60},
	}
	vals := formValues(e)
	if vals.Get("start_time") != "10:00" || vals.Get("repeat") != "weekdays" || vals.Get("private") != "1" || vals.Get("reminders") != "10, 60" {
		t.Fatalf("values = %v", vals)
	}
	p, err := eventPatch(web.NewForm(vals), true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Start.V != "2026-09-10T10:00" || p.End.V != "2026-09-10T10:15" || p.RRule.V != "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR" || p.Visibility.V != domain.VisibilityPrivate {
		t.Fatalf("patch = %+v", p)
	}
	shifted, err := eventPatch(web.NewForm(vals), false, 3)
	if err != nil || shifted.Start.V != "2026-09-07T10:00" || shifted.RRule.Set {
		t.Fatalf("shifted = %+v %v", shifted, err)
	}
}

func TestEventPatchAllDayAndErrors(t *testing.T) {
	f := web.NewForm(url.Values{"all_day": {"1"}, "start_date": {"2026-09-20"}, "end_date": {"2026-09-21"}, "reminders": {""}})
	p, err := eventPatch(f, true, 0)
	if err != nil || p.Start.V != "2026-09-20" || p.End.V != "2026-09-22" || p.Timezone.Set || p.RRule.V != "" {
		t.Fatalf("all-day = %+v %v", p, err)
	}
	_, err = eventPatch(web.NewForm(url.Values{"start_date": {"x"}, "reminders": {"ten"}}), true, 0)
	var ve *domain.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v", err)
	}
	var fields []string
	for _, fe := range ve.Fields {
		fields = append(fields, fe.Field)
	}
	for _, want := range []string{"start", "reminders"} {
		if !slices.Contains(fields, want) {
			t.Errorf("missing %s in %v", want, fields)
		}
	}
}

func TestRepeatValue(t *testing.T) {
	cases := map[string][2]string{
		"":                                 {"none", ""},
		"FREQ=DAILY":                       {"daily", ""},
		"FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR": {"weekdays", ""},
		"FREQ=DAILY;INTERVAL=2":            {"custom", "FREQ=DAILY;INTERVAL=2"},
	}
	for rule, want := range cases {
		v, custom := web.RepeatValue(rule)
		if v != want[0] || custom != want[1] {
			t.Errorf("web.RepeatValue(%q) = %q %q", rule, v, custom)
		}
	}
	f := web.NewForm(url.Values{"repeat": {"custom"}, "rrule": {"FREQ=YEARLY"}})
	if web.RepeatRule(f) != "FREQ=YEARLY" {
		t.Fatal("custom rule must be used")
	}
	if web.RepeatRule(web.NewForm(url.Values{"repeat": {"bogus"}})) != "" {
		t.Fatal("unknown repeat must clear the rule")
	}
}

func TestInstancesAndScopes(t *testing.T) {
	start := time.Date(2026, 9, 7, 7, 0, 0, 0, time.UTC)
	m := domain.Event{ID: domain.NewID(), Start: start, End: start.Add(time.Hour), TZ: "Europe/Moscow", RRule: "FREQ=DAILY"}
	e, inst := atInstance(m, "2026-09-10T07:00:00Z")
	if inst == "" || !e.Start.Equal(start.AddDate(0, 0, 3)) || e.Duration() != time.Hour {
		t.Fatalf("instance = %v %q", e.Start, inst)
	}
	if len(scopesFor(m, inst)) != 3 || scopesFor(m, "") != nil {
		t.Fatal("series scopes mismatch")
	}
	if shiftDays(m, inst, domain.ScopeAll) != 3 || shiftDays(m, inst, domain.ScopeThis) != 0 {
		t.Fatal("shiftDays mismatch")
	}
	single := domain.Event{ID: domain.NewID(), Start: start, End: start.Add(time.Hour), TZ: "UTC"}
	if _, got := atInstance(single, "2026-09-10T07:00:00Z"); got != "" {
		t.Fatal("single events have no instance")
	}
	sid, rid := m.ID, start
	ov := single
	ov.SeriesID, ov.RecurrenceID = &sid, &rid
	if len(scopesFor(ov, "")) != 2 {
		t.Fatal("override scopes mismatch")
	}
}

func TestReminderLabel(t *testing.T) {
	got := web.ReminderLabel([]int{0, 10, 60, 1440})
	if got != "at start, 10 minutes before, 1 hour before, 1 day before" {
		t.Fatalf("label = %q", got)
	}
}

func TestFilter(t *testing.T) {
	work := domain.Calendar{ID: domain.NewID(), Name: "Work", Color: "#3b6ea5"}
	hidden := domain.Calendar{ID: domain.NewID(), Name: "Old", Color: "#aa3355", Hidden: true}
	cals := []domain.Calendar{work, hidden}

	f := parseFilter(url.Values{}, cals)
	if len(f.ids) != 1 || f.ids[0] != work.ID || f.query != "" || !f.todos {
		t.Fatalf("default filter = %+v", f)
	}
	f = parseFilter(url.Values{"f": {"1"}, "cal": {hidden.ID.String(), "junk", hidden.ID.String()}}, cals)
	if len(f.ids) != 1 || f.ids[0] != hidden.ID || f.query != "?cal="+hidden.ID.String()+"&f=1" {
		t.Fatalf("explicit filter = %+v", f)
	}
	if f = parseFilter(url.Values{"f": {"1"}}, cals); len(f.ids) != 0 {
		t.Fatal("empty explicit filter must select nothing")
	}
}
