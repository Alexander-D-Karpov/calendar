package recurrence

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

func timed(start time.Time, dur time.Duration, tz, rule string) domain.Event {
	return domain.Event{Start: start, End: start.Add(dur), TZ: tz, RRule: rule}
}

func starts(t *testing.T, e domain.Event, from, to time.Time) []string {
	t.Helper()
	s, err := FromEvent(e)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Between(from, to, 1000)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(got))
	for i, x := range got {
		if e.AllDay {
			out[i] = x.Format(domain.DateLayout)
		} else {
			out[i] = x.Format(time.RFC3339)
		}
	}
	return out
}

func expectList(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
}

func TestWeeklyAcrossDST(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 19, 10, 0, 0, 0, berlin)
	got := starts(t, timed(start, time.Hour, "Europe/Berlin", "FREQ=WEEKLY"), start, start.AddDate(0, 0, 22))
	expectList(t, got, "2026-10-19T08:00:00Z", "2026-10-26T09:00:00Z", "2026-11-02T09:00:00Z", "2026-11-09T09:00:00Z")
}

func TestAllDayExdateRdate(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	e := domain.Event{
		AllDay: true, Start: start, End: start.AddDate(0, 0, 1), RRule: "FREQ=DAILY;COUNT=5",
		ExDate: []time.Time{start.AddDate(0, 0, 2)}, RDate: []time.Time{start.AddDate(0, 0, 10)},
	}
	got := starts(t, e, start, start.AddDate(0, 1, 0))
	expectList(t, got, "2026-09-01", "2026-09-02", "2026-09-04", "2026-09-05", "2026-09-11")

	s, _ := FromEvent(e)
	if ok, _ := s.Contains(start.AddDate(0, 0, 2)); ok {
		t.Fatal("excluded date must not be an occurrence")
	}
	if ok, _ := s.Contains(start.AddDate(0, 0, 10)); !ok {
		t.Fatal("rdate must be an occurrence")
	}
}

func TestOverlapWindow(t *testing.T) {
	start := time.Date(2026, 9, 10, 23, 0, 0, 0, time.UTC)
	e := timed(start, 2*time.Hour, "UTC", "FREQ=DAILY;COUNT=3")
	got := starts(t, e, start.Add(90*time.Minute), start.Add(24*time.Hour))
	expectList(t, got, "2026-09-10T23:00:00Z")
	if !Overlaps(start, start, start, start.Add(time.Hour)) || Overlaps(start, start, start.Add(-time.Hour), start) {
		t.Fatal("zero length overlap mismatch")
	}
}

func TestSplit(t *testing.T) {
	start := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	at := start.AddDate(0, 0, 2)
	far := start.AddDate(0, 1, 0)

	head, tail, err := Split(timed(start, time.Hour, "UTC", "FREQ=DAILY;COUNT=5"), at)
	if err != nil {
		t.Fatal(err)
	}
	expectList(t, starts(t, timed(start, time.Hour, "UTC", head), start, far), "2026-09-10T10:00:00Z", "2026-09-11T10:00:00Z")
	if got := starts(t, timed(at, time.Hour, "UTC", tail), at, far); len(got) != 3 {
		t.Fatalf("tail = %v", got)
	}

	head, tail, err = Split(timed(start, time.Hour, "UTC", "FREQ=DAILY"), at)
	if err != nil {
		t.Fatal(err)
	}
	expectList(t, starts(t, timed(start, time.Hour, "UTC", head), start, far), "2026-09-10T10:00:00Z", "2026-09-11T10:00:00Z")
	if got := starts(t, timed(at, time.Hour, "UTC", tail), at, at.AddDate(0, 0, 3)); got[0] != "2026-09-12T10:00:00Z" || len(got) != 3 {
		t.Fatalf("tail = %v", got)
	}
}

func TestSpan(t *testing.T) {
	start := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	if _, to := Span(timed(start, time.Hour, "UTC", "FREQ=DAILY")); to != nil {
		t.Fatalf("open series must be unbounded, got %v", to)
	}
	from, to := Span(timed(start, time.Hour, "UTC", "FREQ=DAILY;COUNT=3"))
	if !from.Equal(start) || to == nil || !to.Equal(start.AddDate(0, 0, 2).Add(time.Hour)) {
		t.Fatalf("span = %v %v", from, to)
	}
	day := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	from, to = Span(domain.Event{AllDay: true, Start: day, End: day.AddDate(0, 0, 1)})
	if !from.Equal(day.Add(-allDayMargin)) || !to.Equal(day.AddDate(0, 0, 1).Add(allDayMargin)) {
		t.Fatalf("all-day span = %v %v", from, to)
	}
}

func TestNormalize(t *testing.T) {
	got, err := Normalize("rrule:freq=weekly;byday=mo,we", time.UTC)
	if err != nil || !strings.Contains(got, "FREQ=WEEKLY") || strings.HasPrefix(got, "RRULE:") {
		t.Fatalf("Normalize = %q %v", got, err)
	}
	for _, bad := range []string{"", "nonsense", "FREQ=MINUTELY", "FREQ=DAILY;BYHOUR=9,10", "FREQ=DAILY;COUNT=2;UNTIL=20261001T000000Z"} {
		if _, err := Normalize(bad, time.UTC); err == nil {
			t.Errorf("Normalize(%q) accepted", bad)
		}
	}
	start := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	s, _ := FromEvent(timed(start, time.Hour, "UTC", "FREQ=DAILY"))
	if _, err := s.Between(start, start.AddDate(1, 0, 0), 10); !errors.Is(err, ErrTooMany) {
		t.Fatalf("limit err = %v", err)
	}
}

func TestNext(t *testing.T) {
	day := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	next, rule, ok, err := Next("FREQ=WEEKLY;COUNT=3", day)
	if err != nil || !ok || !next.Equal(day.AddDate(0, 0, 7)) || !strings.Contains(rule, "COUNT=2") {
		t.Fatalf("Next = %v %q %v %v", next, rule, ok, err)
	}
	if _, _, ok, _ := Next("FREQ=DAILY;COUNT=1", day); ok {
		t.Fatal("last occurrence must not repeat")
	}
	if _, _, ok, _ := Next("FREQ=DAILY;UNTIL=20260912T000000Z", day); ok {
		t.Fatal("past UNTIL must not repeat")
	}
	berlin, _ := time.LoadLocation("Europe/Berlin")
	start := time.Date(2026, 10, 24, 18, 0, 0, 0, berlin)
	next, _, _, _ = Next("FREQ=DAILY", start)
	if next.In(berlin).Hour() != 18 || next.In(berlin).Day() != 25 {
		t.Fatalf("dst next = %v", next.In(berlin))
	}
}
