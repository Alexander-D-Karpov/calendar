package yamlfmt

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/transfer"
)

const sample = `format: calendar
schema: 1
settings:
  timezone: Europe/Moscow
  week_start: monday
calendars:
  - name: Work
    color: "#3b6ea5"
    timezone: Europe/Moscow
    events:
      - uid: standup@test
        title: Standup
        body: |
          Agenda and **notes**
        start: 2026-09-07T10:00:00+03:00
        end: 2026-09-07T10:15:00+03:00
        rrule: FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR
        exdates: [2026-09-14T10:00:00+03:00]
        reminders: [10]
        visibility: private
        overrides:
          - recurrence_id: 2026-09-08T10:00:00+03:00
            start: 2026-09-08T11:00:00+03:00
            end: 2026-09-08T11:15:00+03:00
      - title: Conference
        all_day: true
        start: 2026-09-20
        end: 2026-09-22
todo_lists:
  - name: Inbox
    todos:
      - title: Assemble GPU node
        due: 2026-09-12
        time: "18:00"
        duration: 90m
        priority: 2
        checks:
          - text: Riser cables
            done: true
          - text: Thermal paste
        subtasks:
          - title: Order fans
            status: completed
`

func TestDecode(t *testing.T) {
	set, err := Decode([]byte(sample), Options{Timezone: "UTC", MaxItems: 100})
	if err != nil {
		t.Fatal(err)
	}
	if set.Settings == nil || set.Settings.Timezone.V != "Europe/Moscow" || set.Settings.WeekStart.V != 1 {
		t.Fatalf("settings = %+v", set.Settings)
	}
	c := set.Calendars[0]
	if c.Name != "Work" || len(c.Series) != 2 {
		t.Fatalf("calendar = %+v", c)
	}
	m := c.Series[0].Master
	if !m.Start.Equal(time.Date(2026, 9, 7, 7, 0, 0, 0, time.UTC)) || m.TZ != "Europe/Moscow" || m.Visibility != domain.VisibilityPrivate {
		t.Fatalf("master = %+v", m)
	}
	if len(c.Series[0].Overrides) != 1 || c.Series[0].Overrides[0].RecurrenceID == nil {
		t.Fatalf("overrides = %+v", c.Series[0].Overrides)
	}
	conf := c.Series[1].Master
	if !conf.AllDay || conf.End.Sub(conf.Start) != 48*time.Hour {
		t.Fatalf("conference = %+v", conf)
	}
	td := set.Lists[0].Todos[0]
	if td.Todo.Duration != 90 || len(td.Todo.Checks) != 2 || len(td.Subtasks) != 1 || !td.Subtasks[0].Todo.Done() {
		t.Fatalf("todo = %+v", td)
	}
}

func TestRoundTrip(t *testing.T) {
	set, err := Decode([]byte(sample), Options{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := Encode(set, &domain.User{Timezone: "Europe/Moscow", WeekStart: 1, TimeFormat: "24h", DefaultView: "week"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := Decode(out, Options{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	a, b := set.Calendars[0].Series[0], again.Calendars[0].Series[0]
	if !a.Master.Start.Equal(b.Master.Start) || a.Master.RRule != b.Master.RRule || len(a.Master.ExDate) != len(b.Master.ExDate) {
		t.Fatalf("master differs:\n%+v\n%+v", a.Master, b.Master)
	}
	if len(a.Overrides) != len(b.Overrides) || !a.Overrides[0].RecurrenceID.Equal(*b.Overrides[0].RecurrenceID) {
		t.Fatalf("overrides differ: %+v", b.Overrides)
	}
	at, bt := set.Lists[0].Todos[0], again.Lists[0].Todos[0]
	if at.Todo.Duration != bt.Todo.Duration || len(at.Todo.Checks) != len(bt.Todo.Checks) || len(bt.Subtasks) != 1 {
		t.Fatalf("todo differs: %+v", bt)
	}
}

func TestDecodeStrict(t *testing.T) {
	bad := map[string]string{
		"unknown field":  "format: calendar\nschema: 1\ncalendars:\n  - name: x\n    surprise: 1\n",
		"wrong format":   "format: other\nschema: 1\n",
		"future schema":  "format: calendar\nschema: 99\n",
		"empty":          "",
		"two documents":  "format: calendar\nschema: 1\ncalendars: [{name: a}]\n---\nformat: calendar\nschema: 1\n",
		"nothing inside": "format: calendar\nschema: 1\n",
	}
	for name, in := range bad {
		if _, err := Decode([]byte(in), Options{}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	alias := "format: calendar\nschema: 1\nx: &a [1]\ncalendars: [{name: a, events: [{start: 2026-09-07, all_day: true}]}]\n"
	if _, err := Decode([]byte(alias), Options{}); err == nil {
		t.Fatal("an unknown top-level key must be rejected")
	}
	set, err := Decode([]byte("format: calendar\nschema: 1\ncalendars:\n  - name: x\n    events:\n      - title: bad\n        start: nonsense\n"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Problems) != 1 || !strings.Contains(set.Problems[0].Message, "start") {
		t.Fatalf("problems = %+v", set.Problems)
	}
	var _ transfer.Set = set
}

func TestDecodeRejectsAliasBomb(t *testing.T) {
	var b strings.Builder
	b.WriteString("format: calendar\nschema: 1\na0: &a0 [x, x, x, x, x, x, x, x, x, x]\n")
	for i := 1; i <= 9; i++ {
		fmt.Fprintf(&b, "a%d: &a%d [*a%d, *a%d, *a%d, *a%d, *a%d, *a%d, *a%d, *a%d, *a%d, *a%d]\n",
			i, i, i-1, i-1, i-1, i-1, i-1, i-1, i-1, i-1, i-1, i-1)
	}
	done := make(chan error, 1)
	go func() {
		_, err := Decode([]byte(b.String()), Options{})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("an anchor bomb must be refused, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("an anchor bomb must not hang the decoder")
	}
}
