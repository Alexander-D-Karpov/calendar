package view

import (
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/recurrence"
)

const (
	SlotMinutes  = 5
	SlotsPerHour = 60 / SlotMinutes
	SlotsPerDay  = 24 * SlotsPerHour
	MinSpan      = 3
	ShortSpan    = 6
	MaxLanes     = 8
	MaxRows      = 12
)

type Block struct {
	Item
	Row       int
	Span      int
	Lane      int
	Lanes     int
	TimeLabel string
	Short     bool
	Before    bool
	After     bool
}

func (b Block) Class() string {
	return fmt.Sprintf("gr-%d gs-%d lane-%d-%d hex-%s", b.Row, b.Span, b.Lane, b.Lanes, b.Color)
}

type Band struct {
	Row  int
	Span int
}

func (b Band) Class() string {
	return fmt.Sprintf("gr-%d gs-%d", b.Row, b.Span)
}

type Bar struct {
	Item
	Col       int
	Span      int
	Row       int
	Before    bool
	After     bool
	TimeLabel string
}

func (b Bar) Class() string {
	return fmt.Sprintf("gcol-%d gspan-%d grow-%d hex-%s", b.Col+1, b.Span, b.Row, b.Color)
}

type More struct {
	Col   int
	Row   int
	Count int
	Href  string
}

func (m More) Class() string {
	return fmt.Sprintf("gcol-%d grow-%d", m.Col+1, m.Row)
}

func dayBlocks(day time.Time, items []Item, o Options) []Block {
	loc := o.loc()
	start, end := inLoc(day, loc), inLoc(day.AddDate(0, 0, 1), loc)
	var bs []Block
	for _, it := range items {
		if it.Long() || !recurrence.Overlaps(it.Start, it.End, start, end) {
			continue
		}
		from := minuteOf(later(it.Start, start), loc)
		to := 24 * 60
		if it.End.Before(end) {
			to = minuteOf(it.End, loc)
		}
		to = max(to, from)
		row := min(from/SlotMinutes, SlotsPerDay-1)
		span := (to+SlotMinutes-1)/SlotMinutes - row
		span = min(max(span, MinSpan), SlotsPerDay-row)
		bs = append(bs, Block{
			Item:      it,
			Row:       row + 1,
			Span:      span,
			TimeLabel: TimeRange(it.Start, it.End, o),
			Short:     span < ShortSpan,
			Before:    it.Start.Before(start),
			After:     it.End.After(end),
		})
	}
	layoutBlocks(bs)
	return bs
}

func layoutBlocks(bs []Block) {
	slices.SortStableFunc(bs, func(a, b Block) int {
		if a.Row != b.Row {
			return a.Row - b.Row
		}
		return b.Span - a.Span
	})
	start, clusterEnd := 0, 0
	var ends []int
	for i := range bs {
		b := &bs[i]
		if i > start && b.Row >= clusterEnd {
			setLanes(bs[start:i], len(ends))
			start, ends, clusterEnd = i, ends[:0], 0
		}
		lane := slices.IndexFunc(ends, func(e int) bool { return e <= b.Row })
		if lane < 0 {
			lane = len(ends)
			ends = append(ends, 0)
		}
		ends[lane] = b.Row + b.Span
		b.Lane = min(lane, MaxLanes-1)
		clusterEnd = max(clusterEnd, b.Row+b.Span)
	}
	setLanes(bs[start:], len(ends))
}

func setLanes(bs []Block, n int) {
	n = max(1, min(n, MaxLanes))
	for i := range bs {
		bs[i].Lanes = n
	}
}

func toBar(it Item, days []time.Time, loc *time.Location) (Bar, bool) {
	if len(days) == 0 {
		return Bar{}, false
	}
	first, last := it.dates(loc)
	d0, dn := days[0], days[len(days)-1]
	if last.Before(d0) || first.After(dn) {
		return Bar{}, false
	}
	s, e := first, last
	if s.Before(d0) {
		s = d0
	}
	if e.After(dn) {
		e = dn
	}
	c := dayIndex(d0, s)
	return Bar{Item: it, Col: c, Span: dayIndex(d0, e) - c + 1, Before: first.Before(d0), After: last.After(dn)}, true
}

func layoutBars(bars []Bar, cols, maxLanes, rowOffset int) ([]Bar, []int) {
	slices.SortStableFunc(bars, func(a, b Bar) int {
		switch {
		case a.Col != b.Col:
			return a.Col - b.Col
		case a.Long() != b.Long():
			if a.Long() {
				return -1
			}
			return 1
		case a.Span != b.Span:
			return b.Span - a.Span
		}
		return a.Start.Compare(b.Start)
	})
	hidden := make([]int, cols)
	var ends []int
	out := make([]Bar, 0, len(bars))
	for _, b := range bars {
		lane := slices.IndexFunc(ends, func(e int) bool { return e <= b.Col })
		if lane < 0 {
			lane = len(ends)
			ends = append(ends, 0)
		}
		ends[lane] = b.Col + b.Span
		if lane >= maxLanes {
			for c := b.Col; c < b.Col+b.Span && c < cols; c++ {
				hidden[c]++
			}
			continue
		}
		b.Row = lane + rowOffset
		out = append(out, b)
	}
	return out, hidden
}

func moreCells(hidden []int, row int, days []time.Time, o Options) []More {
	var out []More
	for c, n := range hidden {
		if n > 0 {
			out = append(out, More{Col: c, Row: row, Count: n, Href: o.link(Day, days[c])})
		}
	}
	return out
}

func sleepBands(d time.Time, ws []domain.SleepWindow) []Band {
	var out []Band
	prev := d.AddDate(0, 0, -1).Weekday()
	for _, w := range ws {
		if w.Weekday == prev && w.Start > w.End {
			out = appendBand(out, 0, w.End)
		}
		if w.Weekday == d.Weekday() {
			if w.Start < w.End {
				out = appendBand(out, w.Start, w.End)
			} else {
				out = appendBand(out, w.Start, 24*60)
			}
		}
	}
	return out
}

func appendBand(out []Band, from, to int) []Band {
	if to <= from {
		return out
	}
	r := from / SlotMinutes
	return append(out, Band{Row: r + 1, Span: (to+SlotMinutes-1)/SlotMinutes - r})
}

func scrollHour(d time.Time, ws []domain.SleepWindow) int {
	prev := d.AddDate(0, 0, -1).Weekday()
	for _, w := range ws {
		wake := -1
		switch {
		case w.Weekday == prev && w.Start > w.End:
			wake = w.End
		case w.Weekday == d.Weekday() && w.Start < w.End && w.Start < 12*60:
			wake = w.End
		}
		if wake >= 0 {
			return max(0, min(23, wake/60-1))
		}
	}
	return 7
}

func dayIndex(a, b time.Time) int {
	return int(math.Round(b.Sub(a).Hours() / 24))
}

func minuteOf(t time.Time, loc *time.Location) int {
	lt := t.In(loc)
	return lt.Hour()*60 + lt.Minute()
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
