package view

import (
	"slices"
	"time"
)

type AgendaView struct {
	Days []AgendaDay
}

type AgendaDay struct {
	Date  time.Time
	Label string
	Today bool
	Href  string
	Items []AgendaItem
}

type AgendaItem struct {
	Item
	TimeLabel string
}

func BuildAgenda(p Period, items []Item, o Options) *AgendaView {
	loc := o.loc()
	today := o.today()
	last := p.End.AddDate(0, 0, -1)
	by := map[time.Time][]AgendaItem{}
	for _, it := range items {
		first, end := it.dates(loc)
		if first.Before(p.Start) {
			first = p.Start
		}
		if end.After(last) {
			end = last
		}
		label := "All day"
		if !it.AllDay {
			label = TimeRange(it.Start, it.End, o)
		}
		for d := first; !d.After(end); d = d.AddDate(0, 0, 1) {
			by[d] = append(by[d], AgendaItem{Item: it, TimeLabel: label})
		}
	}
	keys := make([]time.Time, 0, len(by))
	for d := range by {
		keys = append(keys, d)
	}
	slices.SortFunc(keys, func(a, b time.Time) int { return a.Compare(b) })
	a := &AgendaView{}
	for _, d := range keys {
		list := by[d]
		slices.SortStableFunc(list, func(x, y AgendaItem) int {
			if x.AllDay != y.AllDay {
				if x.AllDay {
					return -1
				}
				return 1
			}
			return x.Start.Compare(y.Start)
		})
		a.Days = append(a.Days, AgendaDay{Date: d, Label: d.Format("Mon 2 Jan"), Today: d.Equal(today), Href: o.link(Day, d), Items: list})
	}
	return a
}
