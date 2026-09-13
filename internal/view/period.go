package view

import (
	"slices"
	"time"
)

type Kind string

const (
	Day      Kind = "day"
	ThreeDay Kind = "3day"
	Week     Kind = "week"
	Month    Kind = "month"
	Year     Kind = "year"
	Agenda   Kind = "agenda"
)

const (
	dateLayout = "2006-01-02"
	agendaDays = 30
)

var Kinds = []Kind{Day, ThreeDay, Week, Month, Year, Agenda}

func ParseKind(s string) (Kind, bool) {
	k := Kind(s)
	return k, slices.Contains(Kinds, k)
}

func (k Kind) Label() string {
	switch k {
	case Day:
		return "Day"
	case ThreeDay:
		return "3 days"
	case Week:
		return "Week"
	case Month:
		return "Month"
	case Year:
		return "Year"
	}
	return "Agenda"
}

func (k Kind) Key() string {
	switch k {
	case Day:
		return "d"
	case ThreeDay:
		return "3"
	case Week:
		return "w"
	case Month:
		return "m"
	case Year:
		return "y"
	}
	return "a"
}

func (k Kind) Timed() bool {
	return k == Day || k == ThreeDay || k == Week
}

type Period struct {
	Kind   Kind
	Anchor time.Time
	Start  time.Time
	End    time.Time
}

func Date(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func ParseDate(s string) (time.Time, bool) {
	t, err := time.Parse(dateLayout, s)
	return t, err == nil
}

func StartOfWeek(d time.Time, start time.Weekday) time.Time {
	diff := (int(d.Weekday()) - int(start) + 7) % 7
	return d.AddDate(0, 0, -diff)
}

func firstOfMonth(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func NewPeriod(k Kind, anchor time.Time, weekStart time.Weekday) Period {
	a := Date(anchor)
	p := Period{Kind: k, Anchor: a}
	switch k {
	case Day:
		p.Start, p.End = a, a.AddDate(0, 0, 1)
	case ThreeDay:
		p.Start, p.End = a, a.AddDate(0, 0, 3)
	case Week:
		p.Start = StartOfWeek(a, weekStart)
		p.End = p.Start.AddDate(0, 0, 7)
	case Month:
		first := firstOfMonth(a)
		p.Anchor = first
		p.Start = StartOfWeek(first, weekStart)
		p.End = StartOfWeek(first.AddDate(0, 1, -1), weekStart).AddDate(0, 0, 7)
	case Year:
		p.Start = time.Date(a.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
		p.End = p.Start.AddDate(1, 0, 0)
	default:
		p.Kind = Agenda
		p.Start, p.End = a, a.AddDate(0, 0, agendaDays)
	}
	return p
}

func (p Period) Shift(n int) time.Time {
	switch p.Kind {
	case Day:
		return p.Anchor.AddDate(0, 0, n)
	case ThreeDay:
		return p.Anchor.AddDate(0, 0, 3*n)
	case Week:
		return p.Anchor.AddDate(0, 0, 7*n)
	case Month:
		return firstOfMonth(p.Anchor).AddDate(0, n, 0)
	case Year:
		return time.Date(p.Anchor.Year()+n, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return p.Anchor.AddDate(0, 0, agendaDays*n)
}

func (p Period) Days() []time.Time {
	var out []time.Time
	for d := p.Start; d.Before(p.End); d = d.AddDate(0, 0, 1) {
		out = append(out, d)
	}
	return out
}

func (p Period) Contains(d time.Time) bool {
	return !d.Before(p.Start) && d.Before(p.End)
}

func (p Period) Range(loc *time.Location) (time.Time, time.Time) {
	return inLoc(p.Start, loc), inLoc(p.End, loc)
}

func inLoc(d time.Time, loc *time.Location) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
}

func (p Period) Title() string {
	switch p.Kind {
	case Day:
		return p.Start.Format("Monday, 2 January 2006")
	case Month:
		return p.Anchor.Format("January 2006")
	case Year:
		return p.Start.Format("2006")
	}
	return DateRange(p.Start, p.End.AddDate(0, 0, -1))
}

func DateRange(a, b time.Time) string {
	switch {
	case a.Equal(b):
		return a.Format("2 January 2006")
	case a.Year() != b.Year():
		return a.Format("2 Jan 2006") + " – " + b.Format("2 Jan 2006")
	case a.Month() != b.Month():
		return a.Format("2 Jan") + " – " + b.Format("2 Jan 2006")
	}
	return a.Format("2") + " – " + b.Format("2 January 2006")
}

func (p Period) Origin() time.Time {
	if p.Kind == Month {
		return p.Anchor
	}
	return p.Start
}
