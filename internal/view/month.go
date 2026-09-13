package view

import (
	"fmt"
	"time"
)

const monthLanes = 4

type MonthView struct {
	Weekdays []string
	Weeks    []MonthWeek
}

type MonthWeek struct {
	Days []MonthDay
	Bars []Bar
	More []More
}

type MonthDay struct {
	Col     int
	Num     int
	Date    time.Time
	Label   string
	Today   bool
	Outside bool
	Href    string
	NewHref string
}

func (d MonthDay) Class() string {
	return fmt.Sprintf("gcol-%d", d.Col)
}

func BuildMonth(p Period, items []Item, o Options) *MonthView {
	loc := o.loc()
	today := o.today()
	days := p.Days()
	m := &MonthView{}
	for _, d := range days[:7] {
		m.Weekdays = append(m.Weekdays, d.Format("Mon"))
	}
	for w := 0; w+7 <= len(days); w += 7 {
		week := days[w : w+7]
		var bars []Bar
		for _, it := range items {
			b, ok := toBar(it, week, loc)
			if !ok {
				continue
			}
			if !it.Long() {
				b.TimeLabel = o.clock(it.Start)
			}
			bars = append(bars, b)
		}
		visible, hidden := layoutBars(bars, 7, monthLanes, 2)
		mw := MonthWeek{Bars: visible, More: moreCells(hidden, monthLanes+2, week, o)}
		for i, d := range week {
			mw.Days = append(mw.Days, MonthDay{
				Col:     i + 1,
				Num:     d.Day(),
				Date:    d,
				Label:   d.Format("2 January 2006"),
				Today:   d.Equal(today),
				Outside: d.Month() != p.Anchor.Month(),
				Href:    o.link(Day, d),
				NewHref: o.newLink(d, 0, true),
			})
		}
		m.Weeks = append(m.Weeks, mw)
	}
	return m
}

type YearView struct {
	Months []MiniMonth
}

type MiniMonth struct {
	Title    string
	Href     string
	Weekdays []string
	Days     []MiniDay
}

type MiniDay struct {
	Num     int
	Count   int
	Date    time.Time
	Outside bool
	Today   bool
	Href    string
}

func (d MiniDay) Class() string {
	c := fmt.Sprintf("d-%d", level(d.Count))
	if d.Today {
		c += " today"
	}
	return c
}

func (d MiniDay) Title() string {
	return countLabel(d.Count)
}

func BuildYear(p Period, items []Item, o Options) *YearView {
	loc := o.loc()
	today := o.today()
	last := p.End.AddDate(0, 0, -1)
	counts := map[time.Time]int{}
	for _, it := range items {
		first, end := it.dates(loc)
		if first.Before(p.Start) {
			first = p.Start
		}
		if end.After(last) {
			end = last
		}
		for d := first; !d.After(end); d = d.AddDate(0, 0, 1) {
			counts[d]++
		}
	}
	y := &YearView{}
	for mo := time.January; mo <= time.December; mo++ {
		first := time.Date(p.Start.Year(), mo, 1, 0, 0, 0, 0, time.UTC)
		mp := NewPeriod(Month, first, o.WeekStart)
		mm := MiniMonth{Title: first.Format("January"), Href: o.link(Month, first)}
		days := mp.Days()
		for _, d := range days[:7] {
			mm.Weekdays = append(mm.Weekdays, d.Format("Mon")[:2])
		}
		for _, d := range days {
			md := MiniDay{Date: d, Outside: d.Month() != mo}
			if !md.Outside {
				md.Num, md.Count, md.Today, md.Href = d.Day(), counts[d], d.Equal(today), o.link(Day, d)
			}
			mm.Days = append(mm.Days, md)
		}
		y.Months = append(y.Months, mm)
	}
	return y
}

func level(n int) int {
	switch {
	case n == 0:
		return 0
	case n == 1:
		return 1
	case n <= 3:
		return 2
	case n <= 6:
		return 3
	}
	return 4
}
