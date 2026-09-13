package view

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func at(h, m int) time.Time {
	return time.Date(2026, 9, 10, h, m, 0, 0, time.UTC)
}

func TestPeriods(t *testing.T) {
	a := day(2026, 9, 10)
	cases := []struct {
		k          Kind
		start, end time.Time
		title      string
		next       time.Time
	}{
		{Day, a, day(2026, 9, 11), "Thursday, 10 September 2026", day(2026, 9, 11)},
		{ThreeDay, a, day(2026, 9, 13), "10 – 12 September 2026", day(2026, 9, 13)},
		{Week, day(2026, 9, 7), day(2026, 9, 14), "7 – 13 September 2026", day(2026, 9, 17)},
		{Month, day(2026, 8, 31), day(2026, 10, 5), "September 2026", day(2026, 10, 1)},
		{Year, day(2026, 1, 1), day(2027, 1, 1), "2026", day(2027, 1, 1)},
		{Agenda, a, day(2026, 10, 10), "10 Sep – 9 Oct 2026", day(2026, 10, 10)},
	}
	for _, c := range cases {
		p := NewPeriod(c.k, a, time.Monday)
		if !p.Start.Equal(c.start) || !p.End.Equal(c.end) || p.Title() != c.title || !p.Shift(1).Equal(c.next) {
			t.Errorf("%s = %v %v %q next %v", c.k, p.Start, p.End, p.Title(), p.Shift(1))
		}
	}
	if got := NewPeriod(Week, day(2026, 9, 30), time.Monday).Title(); got != "28 Sep – 4 Oct 2026" {
		t.Errorf("cross-month week = %q", got)
	}
	if got := NewPeriod(Week, a, time.Sunday).Start; !got.Equal(day(2026, 9, 6)) {
		t.Errorf("sunday week start = %v", got)
	}
}

func TestLayoutBlocks(t *testing.T) {
	items := []Item{
		{ID: "a", Start: at(10, 0), End: at(11, 0)},
		{ID: "b", Start: at(10, 30), End: at(11, 30)},
		{ID: "c", Start: at(11, 0), End: at(12, 0)},
		{ID: "d", Start: at(13, 0), End: at(13, 5)},
	}
	got := map[string]Block{}
	for _, b := range dayBlocks(day(2026, 9, 10), items, Options{Loc: time.UTC, Clock24: true}) {
		got[b.ID] = b
	}
	want := map[string][4]int{"a": {121, 12, 0, 2}, "b": {127, 12, 1, 2}, "c": {133, 12, 0, 2}, "d": {157, MinSpan, 0, 1}}
	for id, w := range want {
		b := got[id]
		if [4]int{b.Row, b.Span, b.Lane, b.Lanes} != w {
			t.Errorf("%s = %d %d %d %d, want %v", id, b.Row, b.Span, b.Lane, b.Lanes, w)
		}
	}
	if !got["d"].Short || got["a"].Short || got["a"].TimeLabel != "10:00 – 11:00" {
		t.Errorf("labels = %+v", got)
	}
}

func TestSleepAndScroll(t *testing.T) {
	ws := []domain.SleepWindow{
		{Weekday: time.Wednesday, Start: 23*60 + 30, End: 7*60 + 30},
		{Weekday: time.Thursday, Start: 23*60 + 30, End: 7*60 + 30},
	}
	bands := sleepBands(day(2026, 9, 10), ws)
	if len(bands) != 2 || bands[0] != (Band{1, 90}) || bands[1] != (Band{283, 6}) {
		t.Fatalf("bands = %v", bands)
	}
	if h := scrollHour(day(2026, 9, 10), ws); h != 6 {
		t.Fatalf("scroll = %d", h)
	}
	if h := scrollHour(day(2026, 9, 10), nil); h != 7 {
		t.Fatalf("default scroll = %d", h)
	}
}

func TestMonthBars(t *testing.T) {
	p := NewPeriod(Month, day(2026, 9, 10), time.Monday)
	items := []Item{{ID: "x", AllDay: true, Start: day(2026, 9, 6), End: day(2026, 9, 9)}}
	for range 6 {
		items = append(items, Item{ID: "busy", AllDay: true, Start: day(2026, 9, 16), End: day(2026, 9, 17)})
	}
	m := BuildMonth(p, items, Options{Loc: time.UTC})
	if len(m.Weeks) != 5 {
		t.Fatalf("weeks = %d", len(m.Weeks))
	}
	w0, w1 := m.Weeks[0].Bars, m.Weeks[1].Bars
	if len(w0) != 1 || w0[0].Col != 6 || w0[0].Span != 1 || !w0[0].After || w0[0].Row != 2 {
		t.Fatalf("week 0 = %+v", w0)
	}
	if len(w1) != 1 || w1[0].Col != 0 || w1[0].Span != 2 || !w1[0].Before {
		t.Fatalf("week 1 = %+v", w1)
	}
	more := m.Weeks[2].More
	if len(m.Weeks[2].Bars) != monthLanes || len(more) != 1 || more[0].Count != 2 || more[0].Col != 2 || more[0].Row != monthLanes+2 {
		t.Fatalf("week 2 = %d bars, more %+v", len(m.Weeks[2].Bars), more)
	}
}

func TestYearAndAgenda(t *testing.T) {
	items := []Item{
		{ID: "a", Start: at(9, 0), End: at(10, 0)},
		{ID: "b", AllDay: true, Start: day(2026, 9, 10), End: day(2026, 9, 11)},
	}
	y := BuildYear(NewPeriod(Year, day(2026, 9, 10), time.Monday), items, Options{Loc: time.UTC, Now: at(12, 0)})
	var found MiniDay
	for _, d := range y.Months[8].Days {
		if d.Num == 10 {
			found = d
		}
	}
	if found.Count != 2 || !found.Today || found.Class() != "d-2 today" {
		t.Fatalf("mini day = %+v", found)
	}
	ag := BuildAgenda(NewPeriod(Agenda, day(2026, 9, 10), time.Monday), items, Options{Loc: time.UTC, Clock24: true})
	if len(ag.Days) != 1 || len(ag.Days[0].Items) != 2 || ag.Days[0].Items[0].TimeLabel != "All day" {
		t.Fatalf("agenda = %+v", ag.Days)
	}
}

func TestSlotsCSSUpToDate(t *testing.T) {
	got, err := os.ReadFile(filepath.Join("..", "..", "web", "static", "css", "slots.css"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, GridCSS()) {
		t.Fatal("web/static/css/slots.css is stale, run: make css")
	}
}
