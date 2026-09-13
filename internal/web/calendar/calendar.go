package calendar

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/realtime"
	"github.com/Alexander-D-Karpov/calendar/internal/service"
	"github.com/Alexander-D-Karpov/calendar/internal/undo"
	"github.com/Alexander-D-Karpov/calendar/internal/view"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
	"github.com/Alexander-D-Karpov/calendar/internal/web/timeline"
)

const (
	minYear             = 1900
	maxYear             = 2200
	maxLiveMs           = 400 * 24 * time.Hour
	defaultEventMinutes = 60
)

type Store interface {
	Restore(ctx context.Context, owner, id domain.ID, entity string) error
}

type Deps struct {
	Site      *web.Server
	Calendars *service.Calendars
	Events    *service.Events
	Source    timeline.Source
	Hub       *realtime.Hub
	Store     Store
	Undo      *undo.Service
}

type Module struct {
	site   *web.Server
	cals   *service.Calendars
	events *service.Events
	src    timeline.Source
	hub    *realtime.Hub
	store  Store
	undo   *undo.Service
}

func New(d Deps) *Module {
	return &Module{site: d.Site, cals: d.Calendars, events: d.Events, src: d.Source, hub: d.Hub, store: d.Store, undo: d.Undo}
}

func (m *Module) Routes(mux *http.ServeMux) {
	h, user := m.site.Handle, m.site.RequireUser
	mux.Handle("GET /{$}", h(m.home, user))
	mux.Handle("GET /ws", h(m.live, user))
	mux.Handle("GET /cal/{view}", h(m.periodToday, user))
	mux.Handle("GET /cal/{view}/{date}", h(m.showPeriod, user))
	mux.Handle("GET /events/new", h(m.newEvent, user))
	mux.Handle("POST /events", h(m.createEvent, user))
	mux.Handle("GET /events/{id}", h(m.showEvent, user))
	mux.Handle("GET /events/{id}/edit", h(m.editEvent, user))
	mux.Handle("POST /events/{id}/edit", h(m.updateEvent, user))
	mux.Handle("POST /events/{id}/move", h(m.moveEvent, user))
	mux.Handle("POST /events/{id}/delete", h(m.deleteEvent, user))
	mux.Handle("GET "+calendarsPath, h(m.calendarsPage, user))
	mux.Handle("GET "+calendarsPath+"/new", h(m.newCalendar, user))
	mux.Handle("POST "+calendarsPath, h(m.createCalendar, user))
	mux.Handle("GET "+calendarsPath+"/{id}/edit", h(m.editCalendar, user))
	mux.Handle("POST "+calendarsPath+"/{id}/edit", h(m.updateCalendar, user))
	mux.Handle("POST "+calendarsPath+"/{id}/delete", h(m.deleteCalendar, user))
}

func defaultKind(v web.Viewer) view.Kind {
	if k, ok := view.ParseKind(v.User.DefaultView); ok {
		return k
	}
	return view.Week
}

type filter struct {
	ids   []domain.ID
	set   map[domain.ID]bool
	todos bool
	query string
}

func parseFilter(q url.Values, cals []domain.Calendar) filter {
	f := filter{set: map[domain.ID]bool{}, todos: true}
	if q.Get("f") != "1" && len(q["cal"]) == 0 {
		for _, c := range cals {
			if !c.Hidden {
				f.set[c.ID] = true
				f.ids = append(f.ids, c.ID)
			}
		}
		return f
	}
	known := map[domain.ID]bool{}
	for _, c := range cals {
		known[c.ID] = true
	}
	vals := url.Values{"f": {"1"}}
	for _, raw := range q["cal"] {
		id, err := domain.ParseID(raw)
		if err != nil || !known[id] || f.set[id] {
			continue
		}
		f.set[id] = true
		f.ids = append(f.ids, id)
		vals.Add("cal", id.String())
	}
	f.todos = q.Get("todos") == "1"
	if f.todos {
		vals.Set("todos", "1")
	}
	f.query = "?" + vals.Encode()
	return f
}

type viewLink struct {
	Label   string
	Href    string
	Key     string
	Current bool
}

type calendarOption struct {
	domain.Calendar
	Selected bool
	Hex      string
}

type pageView struct {
	view.Rendered
	Title          string
	Kind           view.Kind
	Path           string
	PeriodDate     string
	From           string
	To             string
	Views          []viewLink
	PrevHref       string
	NextHref       string
	TodayHref      string
	NewHref        string
	Calendars      []calendarOption
	ShareCalendars []string
	ShowTodos      bool
	Colors         []string
	Notice         string
	Live           string
	Seq            int64
}

func calPath(k view.Kind, d time.Time, query string) string {
	return "/cal/" + string(k) + "/" + d.Format(domain.DateLayout) + query
}

func newEventLink(d time.Time, minute int, allDay bool) string {
	q := url.Values{"date": {d.Format(domain.DateLayout)}}
	if allDay {
		q.Set("all_day", "1")
	} else {
		q.Set("time", fmt.Sprintf("%02d:%02d", minute/60, minute%60))
	}
	return "/events/new?" + q.Encode()
}

func (m *Module) home(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	m.site.Redirect(w, r, calPath(defaultKind(v), v.Today(), ""))
}

func (m *Module) periodToday(w http.ResponseWriter, r *http.Request) {
	k, ok := view.ParseKind(r.PathValue("view"))
	if !ok {
		m.site.Fail(w, r, domain.ErrNotFound)
		return
	}
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	m.site.Redirect(w, r, calPath(k, v.Today(), ""))
}

func (m *Module) showPeriod(w http.ResponseWriter, r *http.Request) {
	k, ok := view.ParseKind(r.PathValue("view"))
	day, dok := view.ParseDate(r.PathValue("date"))
	if !ok || !dok || day.Year() < minYear || day.Year() > maxYear {
		m.site.Fail(w, r, domain.ErrNotFound)
		return
	}
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	ctx := r.Context()
	cals, err := m.cals.List(ctx, v.User.ID)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	flt := parseFilter(r.URL.Query(), cals)
	ws := time.Weekday(v.User.WeekStart)
	p := view.NewPeriod(k, day, ws)
	res, err := m.src.Load(ctx, timeline.Query{
		Owner:     v.User,
		Loc:       v.Loc,
		Period:    p,
		Calendars: cals,
		Selected:  flt.ids,
		Todos:     flt.todos,
		Sleep:     true,
	})
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	opts := view.Options{
		Loc:       v.Loc,
		Now:       v.Now,
		Clock24:   v.Clock24(),
		WeekStart: ws,
		Sleep:     res.Sleep,
		Link:      func(kind view.Kind, d time.Time) string { return calPath(kind, d, flt.query) },
		NewLink:   newEventLink,
	}
	shareCals := make([]string, len(flt.ids))
	for i, id := range flt.ids {
		shareCals[i] = id.String()
	}
	pv := pageView{
		Rendered:       view.Render(p, res.Items, opts),
		Title:          p.Title(),
		Kind:           k,
		Path:           r.URL.Path,
		PeriodDate:     p.Origin().Format(domain.DateLayout),
		From:           p.Start.Format(domain.DateLayout),
		To:             p.End.AddDate(0, 0, -1).Format(domain.DateLayout),
		ShareCalendars: shareCals,
		Calendars:      calendarOptions(cals, flt),
		ShowTodos:      flt.todos,
		Colors:         view.Colors(res.Items, cals),
		Notice:         res.Notice,
		Live:           timeline.LiveURL("/ws", p, v.Loc),
		Seq:            res.Seq,
	}
	pv.navigation(p, v.Today(), flt.query)
	m.site.RenderBlock(w, r, http.StatusOK, "calendar", web.Block(r, "calendar_view"), v.Page("calendar", p.Title(), pv))
}

func (m *Module) live(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, ferr := time.Parse(time.RFC3339, q.Get("from"))
	to, terr := time.Parse(time.RFC3339, q.Get("to"))
	if ferr != nil || terr != nil || !to.After(from) || to.Sub(from) > maxLiveMs {
		m.site.Fail(w, r, fmt.Errorf("%w: invalid range", domain.ErrInvalid))
		return
	}
	since, _ := strconv.ParseInt(q.Get("since"), 10, 64)
	m.hub.Serve(w, r, realtime.Stream{
		Kind:   "user",
		Owner:  auth.PrincipalFrom(r.Context()).UserID,
		Since:  since,
		Filter: realtime.RangeFilter(from, to),
		Replay: m.src.Changes.ChangesSince,
	})
}

func (pv *pageView) navigation(p view.Period, today time.Time, query string) {
	focus := p.Anchor
	if p.Contains(today) {
		focus = today
	}
	for _, kind := range view.Kinds {
		pv.Views = append(pv.Views, viewLink{Label: kind.Label(), Key: kind.Key(), Href: calPath(kind, focus, query), Current: kind == p.Kind})
	}
	pv.PrevHref = calPath(p.Kind, p.Shift(-1), query)
	pv.NextHref = calPath(p.Kind, p.Shift(1), query)
	pv.TodayHref = calPath(p.Kind, today, query)
	pv.NewHref = newEventLink(focus, 9*60, false)
}

func calendarOptions(cals []domain.Calendar, flt filter) []calendarOption {
	out := make([]calendarOption, len(cals))
	for i, c := range cals {
		out[i] = calendarOption{Calendar: c, Selected: flt.set[c.ID], Hex: view.Hex(c.Color)}
	}
	return out
}
