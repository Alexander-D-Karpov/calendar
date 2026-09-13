package calendar

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/httpx"
	"github.com/Alexander-D-Karpov/calendar/internal/quickadd"
	"github.com/Alexander-D-Karpov/calendar/internal/service"
	"github.com/Alexander-D-Karpov/calendar/internal/view"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

type scopeOption struct {
	Value string
	Label string
}

var (
	scopeThis      = scopeOption{domain.ScopeThis, "This event"}
	scopeFollowing = scopeOption{domain.ScopeFollowing, "This and following events"}
	scopeAll       = scopeOption{domain.ScopeAll, "All events"}
)

type eventView struct {
	Event     domain.Event
	Calendar  domain.Calendar
	When      string
	RuleText  string
	Reminders string
	Instance  string
	Hex       string
	Colors    []string
	Scopes    []scopeOption
	Editable  bool
	EditHref  string
	Back      string
}

type formView struct {
	Action    string
	Cancel    string
	Back      string
	Instance  string
	ETag      string
	Calendars []domain.Calendar
	Repeats   []web.RepeatOption
	Scopes    []scopeOption
}

func (m *Module) showEvent(w http.ResponseWriter, r *http.Request) {
	v, e, inst, ok := m.loadEvent(w, r, r.URL.Query().Get("instance"))
	if !ok {
		return
	}
	cal, err := m.cals.Get(r.Context(), v.User.ID, e.CalendarID)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	color := e.Color
	if color == "" {
		color = cal.Color
	}
	hx := view.Hex(color)
	edit := "/events/" + e.ID.String() + "/edit"
	if inst != "" {
		edit += "?instance=" + url.QueryEscape(inst)
	}
	ev := eventView{
		Event:     e,
		Calendar:  cal,
		When:      whenLabel(e, v),
		RuleText:  e.RRule,
		Reminders: web.ReminderLabel(e.Reminders),
		Instance:  inst,
		Hex:       hx,
		Colors:    []string{hx},
		Scopes:    scopesFor(e, inst),
		Editable:  !cal.ReadOnly && !e.ReadOnly,
		EditHref:  edit,
		Back:      m.backLink(r, v, e),
	}
	title := e.Title
	if title == "" {
		title = "(no title)"
	}
	m.site.RenderBlock(w, r, http.StatusOK, "event", web.Block(r, "event_card"), v.Page("calendar", title, ev))
}

func (m *Module) newEvent(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	cals, err := m.cals.List(r.Context(), v.User.ID)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	q := r.URL.Query()
	day, ok := view.ParseDate(q.Get("date"))
	if !ok {
		day = v.Today()
	}
	minute := 9 * 60
	if t, err := time.Parse("15:04", q.Get("time")); err == nil {
		minute = t.Hour()*60 + t.Minute()
	} else if day.Equal(v.Today()) {
		minute = min(23, v.Now.In(v.Loc).Hour()+1) * 60
	}
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, minute, 0, 0, v.Loc)
	end := start.Add(time.Hour)
	// A drag on empty space sends the slot it covered, so the dialog opens on
	// the span the user actually drew rather than a default hour.
	if t, err := time.Parse("15:04", q.Get("end")); err == nil {
		candidate := time.Date(day.Year(), day.Month(), day.Day(), t.Hour(), t.Minute(), 0, 0, v.Loc)
		if candidate.After(start) {
			end = candidate
		}
	}
	vals := url.Values{
		"start_date": {start.Format(domain.DateLayout)},
		"start_time": {start.Format("15:04")},
		"end_date":   {end.Format(domain.DateLayout)},
		"end_time":   {end.Format("15:04")},
		"timezone":   {v.Loc.String()},
		"repeat":     {"none"},
		"status":     {domain.StatusConfirmed},
	}
	if c := defaultCalendar(cals); c != nil {
		vals.Set("calendar_id", c.ID.String())
		vals.Set("reminders", web.JoinInts(c.DefaultReminders))
		if c.Timezone != "" {
			vals.Set("timezone", c.Timezone)
		}
	}
	if q.Get("all_day") == "1" {
		vals.Set("all_day", "1")
		vals.Set("end_date", vals.Get("start_date"))
		if last, ok := view.ParseDate(q.Get("end_date")); ok && !last.Before(day) {
			vals.Set("end_date", last.Format(domain.DateLayout))
		}
	}
	back := calPath(defaultKind(v), day, "")
	if web.IsFragment(r) {
		back = m.site.BackTo(r, "")
	}
	m.renderForm(w, r, v, http.StatusOK, web.NewForm(vals), formView{Action: "/events", Cancel: back, Back: back}, "New event")
}

func (m *Module) createEvent(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	f, ok := m.site.ParseForm(w, r)
	if !ok {
		return
	}
	if f.Get("quick") == "1" {
		parsed := quickadd.Parse(f.Get("title"), quickadd.Options{Now: v.Now, Loc: v.Loc, WeekStart: time.Weekday(v.User.WeekStart)})
		if parsed.Matched {
			f = f.Merge(quickEventValues(parsed, v))
		}
	}
	back := web.SafeNext(f.Get("back"))
	fv := formView{Action: "/events", Cancel: back, Back: back}
	p, err := eventPatch(f, true, 0)
	var e domain.Event
	if err == nil {
		e, err = m.events.Create(r.Context(), v.User.ID, p)
	}
	if err != nil {
		m.formFail(w, r, v, err, f, fv, "New event")
		return
	}
	token := m.undo.Record(r.Context(), v.User.ID, domain.UndoEventCreate, "Event created.", eventRef{ID: e.ID.String()})
	m.site.FlashUndo(w, r, "ok", "Event created.", token)
	target := calPath(defaultKind(v), eventDay(e, v.Loc), "")
	if web.IsFragment(r) {
		target = back
	}
	m.site.Finish(w, r, target)
}

func (m *Module) editEvent(w http.ResponseWriter, r *http.Request) {
	v, e, inst, ok := m.loadEvent(w, r, r.URL.Query().Get("instance"))
	if !ok {
		return
	}
	scopes := scopesFor(e, inst)
	vals := formValues(e)
	if len(scopes) > 0 {
		vals.Set("scope", scopes[0].Value)
	}
	fv := formView{
		Action:   "/events/" + e.ID.String() + "/edit",
		Cancel:   eventHref(e, inst),
		Back:     m.backLink(r, v, e),
		Instance: inst,
		ETag:     e.ETag(),
		Scopes:   scopes,
	}
	m.renderForm(w, r, v, http.StatusOK, web.NewForm(vals), fv, "Edit event")
}

func (m *Module) updateEvent(w http.ResponseWriter, r *http.Request) {
	id, err := domain.ParseID(r.PathValue("id"))
	if err != nil {
		m.site.Fail(w, r, domain.ErrNotFound)
		return
	}
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	f, ok := m.site.ParseForm(w, r)
	if !ok {
		return
	}
	cur, err := m.events.Get(r.Context(), v.User.ID, id)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	inst, scope := f.Get("instance"), f.Get("scope")
	if scope == "" && inst != "" {
		scope = domain.ScopeThis
	}
	back := web.SafeNext(f.Get("back"))
	fv := formView{
		Action:   "/events/" + id.String() + "/edit",
		Cancel:   back,
		Back:     back,
		Instance: inst,
		ETag:     f.Get("etag"),
		Scopes:   scopesFor(cur, inst),
	}
	allowRule := !cur.IsOverride() && !(cur.IsMaster() && scope == domain.ScopeThis)
	p, err := eventPatch(f, allowRule, shiftDays(cur, inst, scope))
	var e domain.Event
	if err == nil {
		e, err = m.events.Update(r.Context(), v.User.ID, id, p, service.Edit{Scope: scope, Instance: inst, IfMatch: f.Get("etag")})
	}
	if err != nil {
		m.formFail(w, r, v, err, f, fv, "Edit event")
		return
	}
	token := ""
	if scope != domain.ScopeFollowing {
		before, _ := atInstance(cur, inst)
		token = m.undo.Record(r.Context(), v.User.ID, domain.UndoEventRestore, "Event saved.", snapshot(before, scope, inst))
	}
	m.site.FlashUndo(w, r, "ok", "Event saved.", token)
	target := calPath(defaultKind(v), eventDay(e, v.Loc), "")
	if web.IsFragment(r) {
		target = back
	}
	m.site.Finish(w, r, target)
}

func (m *Module) deleteEvent(w http.ResponseWriter, r *http.Request) {
	id, err := domain.ParseID(r.PathValue("id"))
	if err != nil {
		m.site.Fail(w, r, domain.ErrNotFound)
		return
	}
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	f, ok := m.site.ParseForm(w, r)
	if !ok {
		return
	}
	inst, scope := f.Get("instance"), f.Get("scope")
	if scope == "" && inst != "" {
		scope = domain.ScopeThis
	}
	if err := m.events.Delete(r.Context(), v.User.ID, id, service.Edit{Scope: scope, Instance: inst, IfMatch: f.Get("etag")}); err != nil {
		m.site.Fail(w, r, err)
		return
	}
	token := ""
	if scope == "" || scope == domain.ScopeAll {
		token = m.undo.Record(r.Context(), v.User.ID, domain.UndoEventDelete, "Event deleted.", eventRef{ID: id.String()})
	}
	m.site.FlashUndo(w, r, "ok", "Event deleted.", token)
	m.site.Finish(w, r, web.SafeNext(f.Get("back")))
}

func (m *Module) loadEvent(w http.ResponseWriter, r *http.Request, inst string) (web.Viewer, domain.Event, string, bool) {
	id, err := domain.ParseID(r.PathValue("id"))
	if err != nil {
		m.site.Fail(w, r, domain.ErrNotFound)
		return web.Viewer{}, domain.Event{}, "", false
	}
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return web.Viewer{}, domain.Event{}, "", false
	}
	e, err := m.events.Get(r.Context(), v.User.ID, id)
	if err != nil {
		m.site.Fail(w, r, err)
		return web.Viewer{}, domain.Event{}, "", false
	}
	e, inst = atInstance(e, inst)
	return v, e, inst, true
}

// moveEvent retimes an event from the grid: a drag sends start alone, which
// keeps the duration, and an edge resize sends both ends. The grid is drawn in
// the viewer's zone, so the dropped local times are read there and sent as
// instants; the event keeps its own timezone.
func (m *Module) moveEvent(w http.ResponseWriter, r *http.Request) {
	id, err := domain.ParseID(r.PathValue("id"))
	if err != nil {
		m.site.Fail(w, r, domain.ErrNotFound)
		return
	}
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	f, ok := m.site.ParseForm(w, r)
	if !ok {
		return
	}
	var p domain.EventPatch
	at := func(field string) (string, bool) {
		raw := f.Get(field)
		if raw == "" {
			return "", true
		}
		t, err := time.ParseInLocation("2006-01-02T15:04", raw, v.Loc)
		if err != nil {
			return "", false
		}
		return t.Format(time.RFC3339), true
	}
	start, sok := at("start")
	end, eok := at("end")
	if !sok || !eok || (start == "" && end == "") {
		m.site.Fail(w, r, fmt.Errorf("%w: start and end must be local dates and times", domain.ErrInvalid))
		return
	}
	if start != "" {
		p.Start = domain.Some(start)
	}
	if end != "" {
		p.End = domain.Some(end)
	}
	inst, scope := f.Get("instance"), f.Get("scope")
	if scope == "" && inst != "" {
		scope = domain.ScopeThis
	}
	cur, err := m.events.Get(r.Context(), v.User.ID, id)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	_, err = m.events.Update(r.Context(), v.User.ID, id, p, service.Edit{Scope: scope, Instance: inst, IfMatch: f.Get("etag")})
	if err != nil {
		msg := web.ErrorText(err)
		if msg == "" {
			m.site.Fail(w, r, err)
			return
		}
		m.site.Flash(w, r, "error", msg)
		if web.IsFragment(r) {
			w.WriteHeader(httpx.StatusFor(err))
			return
		}
		m.site.Redirect(w, r, m.site.BackTo(r, f.Get("back")))
		return
	}
	before, _ := atInstance(cur, inst)
	token := m.undo.Record(r.Context(), v.User.ID, domain.UndoEventRestore, "Event moved.", snapshot(before, scope, inst))
	m.site.FlashUndo(w, r, "ok", "Event moved.", token)
	if web.IsFragment(r) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	m.site.Redirect(w, r, m.site.BackTo(r, f.Get("back")))
}

func (m *Module) renderForm(w http.ResponseWriter, r *http.Request, v web.Viewer, status int, f *web.Form, fv formView, title string) {
	cals, err := m.cals.List(r.Context(), v.User.ID)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	for _, c := range cals {
		if !c.ReadOnly {
			fv.Calendars = append(fv.Calendars, c)
		}
	}
	fv.Repeats = web.RepeatOptions
	p := v.Page("calendar", title, fv)
	p.Form = f
	m.site.RenderBlock(w, r, status, "event_edit", web.Block(r, "event_form"), p)
}

func (m *Module) formFail(w http.ResponseWriter, r *http.Request, v web.Viewer, err error, f *web.Form, fv formView, title string) {
	m.site.Retry(w, r, err, f, func(status int) { m.renderForm(w, r, v, status, f, fv, title) })
}

func (m *Module) backLink(r *http.Request, v web.Viewer, e domain.Event) string {
	if b := r.URL.Query().Get("back"); b != "" {
		return web.SafeNext(b)
	}
	if web.IsFragment(r) {
		return m.site.BackTo(r, "")
	}
	return calPath(defaultKind(v), eventDay(e, v.Loc), "")
}

func eventPatch(f *web.Form, allowRule bool, shift int) (domain.EventPatch, error) {
	var v domain.ValidationError
	p := domain.EventPatch{
		CalendarID:   domain.Some(f.Get("calendar_id")),
		Title:        domain.Some(f.Get("title")),
		Location:     domain.Some(f.Get("location")),
		URL:          domain.Some(f.Get("url")),
		Body:         domain.Some(strings.TrimRight(f.Values.Get("body"), " \t\r\n")),
		Color:        domain.Some(f.Get("color")),
		Status:       domain.Some(pick(f.Get("status") == domain.StatusTentative, domain.StatusTentative, domain.StatusConfirmed)),
		Visibility:   domain.Some(pick(f.Get("private") == "1", domain.VisibilityPrivate, domain.VisibilityDefault)),
		Transparency: domain.Some(pick(f.Get("free") == "1", domain.TransparencyTransparent, domain.TransparencyOpaque)),
	}
	allDay := f.Get("all_day") == "1"
	p.AllDay = domain.Some(allDay)
	start, ok := view.ParseDate(f.Get("start_date"))
	if !ok {
		v.Add("start", "must be a date")
	}
	end := start
	if s := f.Get("end_date"); s != "" {
		if end, ok = view.ParseDate(s); !ok {
			v.Add("end", "must be a date")
		}
	}
	start, end = start.AddDate(0, 0, -shift), end.AddDate(0, 0, -shift)
	if allDay {
		p.Start = domain.Some(start.Format(domain.DateLayout))
		p.End = domain.Some(end.AddDate(0, 0, 1).Format(domain.DateLayout))
	} else {
		st, et := f.Get("start_time"), f.Get("end_time")
		if st == "" {
			v.Add("start", "time is required for timed events")
		}
		p.Start = domain.Some(start.Format(domain.DateLayout) + "T" + st)
		switch {
		case et == "":
			p.End = domain.Opt[string]{Set: true, Null: true}
		default:
			if end.Equal(start) && et < st {
				end = end.AddDate(0, 0, 1)
			}
			p.End = domain.Some(end.Format(domain.DateLayout) + "T" + et)
		}
		p.Timezone = domain.Some(f.Get("timezone"))
	}
	if allowRule {
		p.RRule = domain.Some(web.RepeatRule(f))
	}
	if mins, ok := web.ParseInts(f.Get("reminders")); ok {
		p.Reminders = domain.Some(mins)
	} else {
		v.Add("reminders", "must be minutes separated by commas, like 10, 60")
	}
	return p, v.Err()
}

func formValues(e domain.Event) url.Values {
	loc := e.Zone()
	v := url.Values{}
	v.Set("title", e.Title)
	v.Set("calendar_id", e.CalendarID.String())
	v.Set("location", e.Location)
	v.Set("url", e.URL)
	v.Set("body", e.Body)
	v.Set("color", e.Color)
	v.Set("reminders", web.JoinInts(e.Reminders))
	v.Set("status", e.Status)
	if e.AllDay {
		v.Set("all_day", "1")
		v.Set("start_date", e.Start.Format(domain.DateLayout))
		v.Set("end_date", e.End.AddDate(0, 0, -1).Format(domain.DateLayout))
	} else {
		start, end := e.Start.In(loc), e.End.In(loc)
		v.Set("start_date", start.Format(domain.DateLayout))
		v.Set("start_time", start.Format(domain.TimeLayout))
		v.Set("end_date", end.Format(domain.DateLayout))
		v.Set("end_time", end.Format(domain.TimeLayout))
		v.Set("timezone", e.TZ)
	}
	if e.Visibility == domain.VisibilityPrivate {
		v.Set("private", "1")
	}
	if e.Transparency == domain.TransparencyTransparent {
		v.Set("free", "1")
	}
	repeat, custom := web.RepeatValue(e.RRule)
	v.Set("repeat", repeat)
	v.Set("rrule", custom)
	return v
}

func atInstance(e domain.Event, inst string) (domain.Event, string) {
	if inst == "" || !e.IsMaster() {
		return e, ""
	}
	t, ok := view.ParseInstance(inst, e.AllDay)
	if !ok {
		return e, ""
	}
	d := e.Duration()
	e.Start, e.End = t, t.Add(d)
	return e, inst
}

func scopesFor(e domain.Event, inst string) []scopeOption {
	switch {
	case e.IsOverride():
		return []scopeOption{scopeThis, scopeFollowing}
	case e.IsMaster() && inst != "":
		return []scopeOption{scopeThis, scopeFollowing, scopeAll}
	}
	return nil
}

func shiftDays(e domain.Event, inst, scope string) int {
	if scope != domain.ScopeAll || !e.IsMaster() || inst == "" {
		return 0
	}
	t, ok := view.ParseInstance(inst, e.AllDay)
	if !ok {
		return 0
	}
	a, b := localDay(e, e.Start), localDay(e, t)
	return int(math.Round(b.Sub(a).Hours() / 24))
}

func localDay(e domain.Event, t time.Time) time.Time {
	if e.AllDay {
		return view.Date(t)
	}
	return view.Date(t.In(e.Zone()))
}

func eventDay(e domain.Event, loc *time.Location) time.Time {
	if e.AllDay {
		return view.Date(e.Start)
	}
	return view.Date(e.Start.In(loc))
}

func eventHref(e domain.Event, inst string) string {
	h := "/events/" + e.ID.String()
	if inst != "" {
		h += "?instance=" + url.QueryEscape(inst)
	}
	return h
}

func whenLabel(e domain.Event, v web.Viewer) string {
	if e.AllDay {
		last := e.End.AddDate(0, 0, -1)
		if !last.After(e.Start) {
			return e.Start.Format("Monday, 2 January 2006")
		}
		return e.Start.Format("Mon 2 Jan") + " – " + last.Format("Mon 2 Jan 2006")
	}
	clock := func(t time.Time) string { return view.Clock(t, v.Clock24()) }
	s, en := e.Start.In(v.Loc), e.End.In(v.Loc)
	out := s.Format("Mon 2 Jan 2006") + " " + clock(s) + " – " + en.Format("Mon 2 Jan 2006") + " " + clock(en)
	if view.Date(s).Equal(view.Date(en)) {
		out = s.Format("Monday, 2 January 2006") + ", " + clock(s) + " – " + clock(en)
	}
	if e.TZ != "" && e.TZ != v.Loc.String() {
		out += " (event timezone " + e.TZ + ")"
	}
	return out
}

func defaultCalendar(cals []domain.Calendar) *domain.Calendar {
	var first *domain.Calendar
	for i := range cals {
		c := &cals[i]
		if c.ReadOnly {
			continue
		}
		if c.IsDefault {
			return c
		}
		if first == nil {
			first = c
		}
	}
	return first
}

func pick(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}

func quickEventValues(p quickadd.Result, v web.Viewer) url.Values {
	day := v.Today()
	if p.Date != nil {
		day = *p.Date
	}
	out := url.Values{}
	out.Set("title", p.Title)
	out.Set("start_date", day.Format(domain.DateLayout))
	out.Set("end_date", day.Format(domain.DateLayout))
	out.Set("timezone", v.Loc.String())
	if p.Time == nil {
		out.Set("all_day", "1")
		return out
	}
	start := *p.Time
	out.Set("start_time", start.String())
	end := start.Add(defaultEventMinutes)
	switch {
	case p.End != nil && p.End.Minutes() > start.Minutes():
		end = *p.End
	case p.Duration > 0:
		end = start.Add(p.Duration)
	}
	out.Set("end_time", end.String())
	if end.Minutes() <= start.Minutes() {
		out.Set("end_date", day.AddDate(0, 0, 1).Format(domain.DateLayout))
	}
	return out
}
