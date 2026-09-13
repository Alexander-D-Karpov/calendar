package domain

import "time"

// TimeOfDay is a wall-clock time with no date attached, used where a time is
// chosen on its own: a todo due time, or the start typed into quick add.
type TimeOfDay struct {
	Hour   int
	Minute int
}

func (t TimeOfDay) Minutes() int {
	return t.Hour*60 + t.Minute
}

func (t TimeOfDay) String() string {
	return FormatClock(t.Minutes())
}

// Add moves the clock forward, wrapping past midnight. Callers that care about
// the day rolling over compare the result against the start.
func (t TimeOfDay) Add(minutes int) TimeOfDay {
	m := ((t.Minutes()+minutes)%minutesPerDay + minutesPerDay) % minutesPerDay
	return TimeOfDay{Hour: m / 60, Minute: m % 60}
}

const minutesPerDay = 24 * 60

// DayOf truncates to midnight in the value's own location, so two days built
// from the same zone stay comparable. view.Date differs: it moves the date to
// UTC for storage.
func DayOf(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
