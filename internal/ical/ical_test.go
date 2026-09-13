package ical

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/transfer"
)

const sample = `BEGIN:VCALENDAR
PRODID:-//Test//EN
VERSION:2.0
X-WR-CALNAME:Work
X-WR-TIMEZONE:Europe/Moscow
BEGIN:VEVENT
UID:standup@test
DTSTAMP:20260901T090000Z
SUMMARY:Standup
DESCRIPTION:Agenda\nand notes
LOCATION:Office
DTSTART;TZID=Europe/Moscow:20260907T100000
DTEND;TZID=Europe/Moscow:20260907T101500
RRULE:FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR
EXDATE;TZID=Europe/Moscow:20260914T100000
BEGIN:VALARM
ACTION:DISPLAY
TRIGGER;VALUE=DURATION:-PT10M
END:VALARM
END:VEVENT
BEGIN:VEVENT
UID:standup@test
DTSTAMP:20260901T090000Z
RECURRENCE-ID;TZID=Europe/Moscow:20260908T100000
SUMMARY:Standup moved
DTSTART;TZID=Europe/Moscow:20260908T110000
DTEND;TZID=Europe/Moscow:20260908T111500
END:VEVENT
BEGIN:VEVENT
UID:standup@test
DTSTAMP:20260901T090000Z
RECURRENCE-ID;TZID=Europe/Moscow:20260909T100000
STATUS:CANCELLED
DTSTART;TZID=Europe/Moscow:20260909T100000
END:VEVENT
BEGIN:VEVENT
UID:conf@test
DTSTAMP:20260901T090000Z
SUMMARY:Conference
DTSTART;VALUE=DATE:20260920
DTEND;VALUE=DATE:20260922
CLASS:PRIVATE
TRANSP:TRANSPARENT
END:VEVENT
BEGIN:VEVENT
UID:lunch@test
DTSTAMP:20260901T090000Z
SUMMARY:Lunch
DTSTART:20260907T110000Z
DURATION:PT1H30M
END:VEVENT
BEGIN:VEVENT
UID:broken@test
DTSTAMP:20260901T090000Z
SUMMARY:Broken
END:VEVENT
BEGIN:VTODO
UID:gpu@test
DTSTAMP:20260901T090000Z
SUMMARY:Assemble GPU node
DUE;TZID=Europe/Moscow:20260912T180000
PRIORITY:1
END:VTODO
BEGIN:VTODO
UID:fans@test
DTSTAMP:20260901T090000Z
SUMMARY:Order fans
RELATED-TO:gpu@test
STATUS:COMPLETED
COMPLETED:20260910T120000Z
END:VTODO
END:VCALENDAR
`

func TestDecode(t *testing.T) {
	set, err := Decode([]byte(sample), Options{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	c := set.Calendars[0]
	if c.Name != "Work" || c.Timezone != "Europe/Moscow" || len(c.Series) != 3 {
		t.Fatalf("calendar = %q %q %d series", c.Name, c.Timezone, len(c.Series))
	}
	if len(set.Problems) != 1 || !strings.Contains(set.Problems[0].Where, "Broken") {
		t.Fatalf("problems = %+v", set.Problems)
	}
	var std transfer.Series
	for _, s := range c.Series {
		if s.Master.UID == "standup@test" {
			std = s
		}
	}
	m := std.Master
	if !m.Start.Equal(time.Date(2026, 9, 7, 7, 0, 0, 0, time.UTC)) || m.TZ != "Europe/Moscow" || m.Body != "Agenda\nand notes" {
		t.Fatalf("master = %+v", m)
	}
	if !slices.Equal(m.Reminders, []int{10}) || len(m.ExDate) != 2 || len(std.Overrides) != 1 {
		t.Fatalf("recurrence = %v %v %d", m.Reminders, m.ExDate, len(std.Overrides))
	}
	if ov := std.Overrides[0]; ov.RecurrenceID == nil || !ov.RecurrenceID.Equal(time.Date(2026, 9, 8, 7, 0, 0, 0, time.UTC)) {
		t.Fatalf("override = %+v", std.Overrides[0])
	}
	for _, s := range c.Series {
		switch s.Master.UID {
		case "conf@test":
			e := s.Master
			if !e.AllDay || e.Visibility != domain.VisibilityPrivate || e.Transparency != domain.TransparencyTransparent || e.End.Sub(e.Start) != 48*time.Hour {
				t.Fatalf("conference = %+v", e)
			}
		case "lunch@test":
			if d := s.Master.Duration(); d != 90*time.Minute {
				t.Fatalf("duration = %v", d)
			}
		}
	}
	todos := set.Lists[0].Todos
	if len(todos) != 1 || len(todos[0].Subtasks) != 1 {
		t.Fatalf("todos = %+v", todos)
	}
	gpu := todos[0].Todo
	if gpu.DueDate == nil || gpu.DueTime == nil || *gpu.DueTime != 18*60 || gpu.TZ != "Europe/Moscow" || gpu.Priority != 3 || !gpu.ShowOnCalendar {
		t.Fatalf("todo = %+v", gpu)
	}
	if sub := todos[0].Subtasks[0].Todo; !sub.Done() || sub.CompletedAt == nil {
		t.Fatalf("subtask = %+v", sub)
	}
}

func TestRoundTrip(t *testing.T) {
	set, err := Decode([]byte(sample), Options{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := Encode(set, "Work")
	if err != nil {
		t.Fatal(err)
	}
	again, err := Decode(out, Options{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Calendars[0].Series) != len(set.Calendars[0].Series) || len(again.Lists[0].Todos) != 1 {
		t.Fatalf("round trip lost entries: %d series", len(again.Calendars[0].Series))
	}
	var a, b transfer.Series
	for _, s := range set.Calendars[0].Series {
		if s.Master.UID == "standup@test" {
			a = s
		}
	}
	for _, s := range again.Calendars[0].Series {
		if s.Master.UID == "standup@test" {
			b = s
		}
	}
	if !a.Master.Start.Equal(b.Master.Start) || a.Master.TZ != b.Master.TZ || a.Master.RRule != b.Master.RRule {
		t.Fatalf("master differs:\n%+v\n%+v", a.Master, b.Master)
	}
	if len(a.Master.ExDate) != len(b.Master.ExDate) || len(a.Overrides) != len(b.Overrides) {
		t.Fatalf("exdates or overrides differ: %v %v", b.Master.ExDate, b.Overrides)
	}
	if !slices.Equal(a.Master.Reminders, b.Master.Reminders) {
		t.Fatalf("reminders = %v", b.Master.Reminders)
	}
	if !strings.Contains(string(out), "BEGIN:VTODO") || !strings.Contains(string(out), "DTSTART;VALUE=DATE:20260920") {
		t.Fatal("encoded output is missing todos or all-day dates")
	}
}

func TestParseDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"PT15M":     15 * time.Minute,
		"-PT10M":    -10 * time.Minute,
		"P1DT2H30M": 26*time.Hour + 30*time.Minute,
		"P2W":       14 * 24 * time.Hour,
		"PT1H30M0S": 90 * time.Minute,
	}
	for in, want := range cases {
		got, err := parseDuration(in)
		if err != nil || got != want {
			t.Errorf("parseDuration(%q) = %v %v, want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "P", "15M", "PT15X", "P1M", "PTM"} {
		if _, err := parseDuration(bad); err == nil {
			t.Errorf("parseDuration(%q) accepted", bad)
		}
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "not a calendar", "BEGIN:VCALENDAR\n"} {
		if _, err := Decode([]byte(bad), Options{}); err == nil {
			t.Errorf("Decode(%q) accepted", bad)
		}
	}
	if _, err := Decode([]byte(sample), Options{MaxItems: 3}); err == nil {
		t.Fatal("the item cap must be enforced")
	}
}

// SEQUENCE is stored in an int32 column. A feed is remote input, so an
// oversized or negative value has to be clamped at the door instead of
// wrapping to a negative number several layers later.
func TestDecodeClampsSequence(t *testing.T) {
	const tmpl = `BEGIN:VCALENDAR
PRODID:-//Test//EN
VERSION:2.0
BEGIN:VEVENT
UID:seq@test
DTSTAMP:20260901T090000Z
SUMMARY:Sequence
DTSTART:20260907T100000Z
DTEND:20260907T110000Z
SEQUENCE:%s
END:VEVENT
END:VCALENDAR
`
	cases := []struct {
		raw  string
		want int
	}{
		{"0", 0},
		{"7", 7},
		{"2147483648", domain.MaxSequence},
		{"9999999999999999999", 0},
		{"-5", 0},
	}
	for _, c := range cases {
		set, err := Decode([]byte(strings.Replace(tmpl, "%s", c.raw, 1)), Options{Timezone: "UTC"})
		if err != nil {
			t.Fatalf("SEQUENCE:%s did not decode: %v", c.raw, err)
		}
		got := set.Calendars[0].Series[0].Master.Sequence
		if got != c.want {
			t.Errorf("SEQUENCE:%s decoded to %d, want %d", c.raw, got, c.want)
		}
		if got < 0 || got > domain.MaxSequence {
			t.Errorf("SEQUENCE:%s left %d outside [0,%d]", c.raw, got, domain.MaxSequence)
		}
	}
}
