package exporter

import (
	"testing"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

func at(day, hour int) time.Time {
	return time.Date(2026, 9, day, hour, 0, 0, 0, time.UTC)
}

func TestOverlaps(t *testing.T) {
	from, to := at(10, 0), at(17, 0)
	cases := map[string]struct {
		e    domain.Event
		want bool
	}{
		"inside":            {domain.Event{Start: at(12, 9), End: at(12, 10)}, true},
		"before":            {domain.Event{Start: at(1, 9), End: at(1, 10)}, false},
		"after":             {domain.Event{Start: at(20, 9), End: at(20, 10)}, false},
		"straddles start":   {domain.Event{Start: at(9, 23), End: at(10, 1)}, true},
		"straddles end":     {domain.Event{Start: at(16, 23), End: at(17, 1)}, true},
		"ends at from":      {domain.Event{Start: at(9, 8), End: at(10, 0)}, false},
		"starts at to":      {domain.Event{Start: at(17, 0), End: at(17, 1)}, false},
		"open-ended series": {domain.Event{Start: at(1, 9), End: at(1, 10), RRule: "FREQ=WEEKLY"}, true},
	}
	for name, c := range cases {
		if got := overlaps(c.e, from, to); got != c.want {
			t.Errorf("%s: overlaps = %v, want %v", name, got, c.want)
		}
	}
}
