package view

import (
	"net/url"
	"strconv"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
)

type Item struct {
	ID           string
	Instance     string
	Title        string
	Location     string
	Excerpt      string
	Color        string
	CalendarName string
	AllDay       bool
	Tentative    bool
	Private      bool
	Todo         bool
	Done         bool
	Static       bool
	ReadOnly     bool
	ChecksDone   int
	ChecksTotal  int
	Start        time.Time
	End          time.Time
}

func (i Item) Href() string {
	switch {
	case i.Static:
		return ""
	case i.Todo:
		return "/todos/" + i.ID
	}
	h := "/events/" + i.ID
	if i.Instance != "" {
		h += "?instance=" + url.QueryEscape(i.Instance)
	}
	return h
}

func (i Item) ToggleHref() string {
	if i.Static {
		return ""
	}
	return "/todos/" + i.ID + "/toggle"
}

// MoveHref is the endpoint that drops the item at a new time. Empty when the
// item cannot be dragged: read-only share views and todos, which carry their
// due time rather than a span.
func (i Item) MoveHref() string {
	if i.Static || i.ReadOnly {
		return ""
	}
	if i.Todo {
		return "/todos/" + i.ID + "/move-time"
	}
	return "/events/" + i.ID + "/move"
}

func (i Item) Progress() string {
	if i.ChecksTotal == 0 {
		return ""
	}
	return strconv.Itoa(i.ChecksDone) + "/" + strconv.Itoa(i.ChecksTotal)
}

func (i Item) DisplayTitle() string {
	if i.Title == "" {
		return "(no title)"
	}
	return i.Title
}

func (i Item) Long() bool {
	return i.AllDay || i.End.Sub(i.Start) >= 24*time.Hour
}

func (i Item) dates(loc *time.Location) (time.Time, time.Time) {
	var first, last time.Time
	if i.AllDay {
		first, last = Date(i.Start), Date(i.End).AddDate(0, 0, -1)
	} else {
		s, e := i.Start.In(loc), i.End.In(loc)
		if e.After(s) {
			e = e.Add(-time.Nanosecond)
		}
		first, last = Date(s), Date(e)
	}
	if last.Before(first) {
		last = first
	}
	return first, last
}

type Options struct {
	Loc       *time.Location
	Now       time.Time
	Clock24   bool
	WeekStart time.Weekday
	Sleep     []domain.SleepWindow
	Link      func(Kind, time.Time) string
	NewLink   func(day time.Time, minute int, allDay bool) string
}

func (o Options) loc() *time.Location {
	if o.Loc == nil {
		return time.UTC
	}
	return o.Loc
}

func (o Options) today() time.Time {
	return Date(o.Now.In(o.loc()))
}

func (o Options) link(k Kind, d time.Time) string {
	if o.Link == nil {
		return ""
	}
	return o.Link(k, d)
}

func (o Options) newLink(d time.Time, minute int, allDay bool) string {
	if o.NewLink == nil {
		return ""
	}
	return o.NewLink(d, minute, allDay)
}

func (o Options) clock(t time.Time) string {
	return Clock(t.In(o.loc()), o.Clock24)
}

func Clock(t time.Time, clock24 bool) string {
	if clock24 {
		return t.Format("15:04")
	}
	return t.Format("3:04 PM")
}

func TimeRange(a, b time.Time, o Options) string {
	if a.Equal(b) {
		return o.clock(a)
	}
	return o.clock(a) + " – " + o.clock(b)
}

func hourLabel(h int, clock24 bool) string {
	t := time.Date(2000, 1, 1, h, 0, 0, 0, time.UTC)
	if clock24 {
		return t.Format("15:04")
	}
	return t.Format("3 PM")
}

func countLabel(n int) string {
	if n == 1 {
		return "1 event"
	}
	return strconv.Itoa(n) + " events"
}
