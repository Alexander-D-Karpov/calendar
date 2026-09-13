package csvfmt

import (
	"strings"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

const google = "Subject,Start Date,Start Time,End Date,End Time,All Day Event,Description,Location,Private\n" +
	"Standup,09/07/2026,10:00 AM,09/07/2026,10:15 AM,False,Agenda,Office,False\n" +
	"Conference,09/20/2026,,09/21/2026,,True,,Vilnius,True\n" +
	"Broken,notadate,,,,,,,\n"

func TestDecodeGoogle(t *testing.T) {
	set, err := Decode([]byte(google), Options{Timezone: "Europe/Moscow", MaxItems: 100})
	if err != nil {
		t.Fatal(err)
	}
	series := set.Calendars[0].Series
	if len(series) != 2 || len(set.Problems) != 1 {
		t.Fatalf("series = %d problems = %+v", len(series), set.Problems)
	}
	std := series[0].Master
	if !std.Start.Equal(time.Date(2026, 9, 7, 7, 0, 0, 0, time.UTC)) || std.Duration() != 15*time.Minute || std.TZ != "Europe/Moscow" {
		t.Fatalf("standup = %+v", std)
	}
	conf := series[1].Master
	if !conf.AllDay || conf.End.Sub(conf.Start) != 48*time.Hour || conf.Visibility != domain.VisibilityPrivate {
		t.Fatalf("conference = %+v", conf)
	}
}

func TestDecodeVariants(t *testing.T) {
	semi := "Тема;Дата начала;Время начала\nВстреча;12.09.2026;18:00\n"
	set, err := Decode([]byte(semi), Options{Timezone: "UTC"})
	if err == nil {
		t.Fatalf("a header without a known title column must fail, got %+v", set)
	}
	tabs := "Title\tStart\tTime\nStandup\t2026-09-07\t10:00\n"
	set, err = Decode([]byte(tabs), Options{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if e := set.Calendars[0].Series[0].Master; e.Title != "Standup" || e.Start.Hour() != 10 {
		t.Fatalf("tab separated = %+v", e)
	}
	dayFirst, err := Decode([]byte("Subject,Start Date\nx,07/09/2026\n"), Options{Timezone: "UTC", DayFirst: true})
	if err != nil {
		t.Fatal(err)
	}
	if e := dayFirst.Calendars[0].Series[0].Master; e.Start.Month() != time.September || e.Start.Day() != 7 {
		t.Fatalf("day first = %v", e.Start)
	}
	if _, err := Decode([]byte("nothing\n"), Options{}); err == nil {
		t.Fatal("a file without usable columns must fail")
	}
}

func TestEncodeRoundTrip(t *testing.T) {
	set, err := Decode([]byte(google), Options{Timezone: "Europe/Moscow"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := Encode(set)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(out), bom) {
		t.Fatal("the export must start with a BOM so Excel reads UTF-8")
	}
	again, err := Decode(out, Options{Timezone: "Europe/Moscow"})
	if err != nil {
		t.Fatal(err)
	}
	a, b := set.Calendars[0].Series, again.Calendars[0].Series
	if len(a) != len(b) {
		t.Fatalf("round trip lost rows: %d", len(b))
	}
	for i := range a {
		if !a[i].Master.Start.Equal(b[i].Master.Start) || !a[i].Master.End.Equal(b[i].Master.End) || a[i].Master.AllDay != b[i].Master.AllDay {
			t.Fatalf("row %d differs:\n%+v\n%+v", i, a[i].Master, b[i].Master)
		}
	}
}
