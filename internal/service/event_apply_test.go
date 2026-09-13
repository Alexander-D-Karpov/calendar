package service

import (
	"slices"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

func blankEvent() domain.Event {
	return domain.Event{
		UID: "u", TZ: "Europe/Moscow", Status: domain.StatusConfirmed,
		Transparency: domain.TransparencyOpaque, Visibility: domain.VisibilityDefault,
	}
}

func TestApplyEventTiming(t *testing.T) {
	e := blankEvent()
	if err := applyEvent(&e, domain.EventPatch{Title: domain.Some(" Standup "), Start: domain.Some("2026-09-10T10:00")}, true); err != nil {
		t.Fatal(err)
	}
	if e.Title != "Standup" || !e.Start.Equal(time.Date(2026, 9, 10, 7, 0, 0, 0, time.UTC)) || e.Duration() != time.Hour {
		t.Fatalf("created = %+v", e)
	}
	e.End = e.Start.Add(15 * time.Minute)
	if err := applyEvent(&e, domain.EventPatch{Start: domain.Some("2026-09-10T12:00:00+03:00")}, false); err != nil {
		t.Fatal(err)
	}
	if !e.Start.Equal(time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)) || e.Duration() != 15*time.Minute || e.Sequence != 1 {
		t.Fatalf("moved = %+v", e)
	}

	if got := fieldNames(t, applyEvent(&e, domain.EventPatch{AllDay: domain.Some(true)}, false)); !slices.Equal(got, []string{"start"}) {
		t.Fatalf("switch without start = %v", got)
	}
	e2 := blankEvent()
	if err := applyEvent(&e2, domain.EventPatch{AllDay: domain.Some(true), Start: domain.Some("2026-09-20")}, true); err != nil {
		t.Fatal(err)
	}
	if !e2.AllDay || e2.End.Sub(e2.Start) != 24*time.Hour || e2.Start.Location() != time.UTC {
		t.Fatalf("all-day = %+v", e2)
	}
}

func TestApplyEventValidation(t *testing.T) {
	e := blankEvent()
	err := applyEvent(&e, domain.EventPatch{
		Start:  domain.Some("2026-09-10T10:00:00Z"),
		End:    domain.Some("2026-09-10T09:00:00Z"),
		URL:    domain.Some("javascript:alert(1)"),
		Status: domain.Some("maybe"),
		Color:  domain.Some("blue"),
		RRule:  domain.Some("FREQ=SECONDLY"),
	}, true)
	got := fieldNames(t, err)
	for _, f := range []string{"end", "url", "status", "color", "rrule"} {
		if !slices.Contains(got, f) {
			t.Errorf("missing %s in %v", f, got)
		}
	}

	sid, rid := domain.NewID(), time.Now()
	ov := blankEvent()
	ov.SeriesID, ov.RecurrenceID = &sid, &rid
	ov.Start, ov.End = rid, rid.Add(time.Hour)
	if got := fieldNames(t, applyEvent(&ov, domain.EventPatch{RRule: domain.Some("FREQ=DAILY")}, false)); !slices.Equal(got, []string{"rrule"}) {
		t.Fatalf("override rrule = %v", got)
	}
	if got := fieldNames(t, applyEvent(&ov, domain.EventPatch{UID: domain.Some("x")}, false)); !slices.Equal(got, []string{"uid"}) {
		t.Fatalf("uid change = %v", got)
	}
}

func TestParseRangeAndWindow(t *testing.T) {
	msk, _ := time.LoadLocation("Europe/Moscow")
	var v domain.ValidationError
	from, to := parseRange(&v, "2026-09-07", "2026-09-14T00:00:00 03:00", msk)
	if v.Err() != nil || !from.Equal(time.Date(2026, 9, 6, 21, 0, 0, 0, time.UTC)) || !to.Equal(time.Date(2026, 9, 13, 21, 0, 0, 0, time.UTC)) {
		t.Fatalf("range = %v %v %v", from, to, v.Err())
	}
	w := newWindow(from, to, msk)
	if !w.dayFrom.Equal(time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)) || !w.dayTo.Equal(time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("days = %v %v", w.dayFrom, w.dayTo)
	}
	var bad domain.ValidationError
	parseRange(&bad, "", "2027-12-01", msk)
	parseRange(&bad, "2026-09-10", "2026-09-01", msk)
	if got := len(bad.Fields); got != 2 {
		t.Fatalf("errors = %v", bad.Fields)
	}
}
