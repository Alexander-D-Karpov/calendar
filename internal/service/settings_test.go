package service

import (
	"slices"
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

func TestApplySettings(t *testing.T) {
	u := domain.User{Timezone: "UTC", TimeFormat: "24h", DefaultView: "week", WeekStart: 1}
	var ws []domain.SleepWindow
	err := applySettings(&u, &ws, domain.SettingsPatch{
		Timezone:   domain.Some("Europe/Vilnius"),
		TimeFormat: domain.Some("12h"),
		Sleep: domain.Some(domain.SleepInput{Enabled: true, Windows: []domain.SleepWindowInput{
			{Weekday: 3, Start: "23:30", End: "07:30"},
			{Weekday: 1, Start: "22:00", End: "06:00"},
		}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if u.Timezone != "Europe/Vilnius" || u.TimeFormat != "12h" || !u.SleepEnabled || len(ws) != 2 || ws[0].Weekday != time.Monday || ws[1].Start != 23*60+30 {
		t.Fatalf("user = %+v windows = %+v", u, ws)
	}
	err = applySettings(&u, &ws, domain.SettingsPatch{
		Timezone:    domain.Some("Mars/Base"),
		WeekStart:   domain.Some(9),
		DefaultView: domain.Some("decade"),
		Sleep: domain.Some(domain.SleepInput{Enabled: true, Windows: []domain.SleepWindowInput{
			{Weekday: 1, Start: "22:00", End: "22:00"},
			{Weekday: 2, Start: "x", End: "y"},
		}}),
	})
	got := fieldNames(t, err)
	for _, f := range []string{"timezone", "week_start", "default_view", "sleep"} {
		if !slices.Contains(got, f) {
			t.Errorf("missing %s in %v", f, got)
		}
	}
}
