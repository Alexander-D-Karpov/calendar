package view

import "time"

const allDayLanes = 3

type Grid struct {
	Columns    []Column
	Bars       []Bar
	More       []More
	Hours      []Hour
	ScrollHour int
	Today      string
	TZ         string
}

type Hour struct {
	Label string
	Row   int
}

type Slot struct {
	Row   int
	Label string
	Href  string
}

type Column struct {
	Date    time.Time
	Weekday string
	Num     int
	Today   bool
	Weekend bool
	Href    string
	Slots   []Slot
	Blocks  []Block
	Sleep   []Band
	NowRow  int
}

func BuildGrid(p Period, items []Item, o Options) *Grid {
	loc := o.loc()
	days := p.Days()
	today := o.today()
	g := &Grid{TZ: loc.String(), Today: today.Format(dateLayout)}

	var bars []Bar
	for _, it := range items {
		if !it.Long() {
			continue
		}
		if b, ok := toBar(it, days, loc); ok {
			bars = append(bars, b)
		}
	}
	var hidden []int
	g.Bars, hidden = layoutBars(bars, len(days), allDayLanes, 1)
	g.More = moreCells(hidden, allDayLanes+1, days, o)

	for h := range 24 {
		g.Hours = append(g.Hours, Hour{Label: hourLabel(h, o.Clock24), Row: h*SlotsPerHour + 1})
	}
	focus := days[0]
	for _, d := range days {
		c := Column{
			Date:    d,
			Weekday: d.Format("Mon"),
			Num:     d.Day(),
			Today:   d.Equal(today),
			Weekend: d.Weekday() == time.Saturday || d.Weekday() == time.Sunday,
			Href:    o.link(Day, d),
			Blocks:  dayBlocks(d, items, o),
			Sleep:   sleepBands(d, o.Sleep),
		}
		for _, h := range g.Hours {
			c.Slots = append(c.Slots, Slot{Row: h.Row, Label: h.Label, Href: o.newLink(d, (h.Row-1)*SlotMinutes, false)})
		}
		if c.Today {
			n := o.Now.In(loc)
			c.NowRow = (n.Hour()*60+n.Minute())/SlotMinutes + 1
			focus = d
		}
		g.Columns = append(g.Columns, c)
	}
	g.ScrollHour = scrollHour(focus, o.Sleep)
	return g
}
