package calendar

import (
	"net/http"
	"net/url"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/view"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

const calendarsPath = "/settings/calendars"

type calendarRow struct {
	domain.Calendar
	Hex       string
	Reminders string
}

type calendarsView struct {
	Rows   []calendarRow
	Colors []string
}

type calendarFormView struct {
	Action string
	Cancel string
	Back   string
	ETag   string
}

func (m *Module) calendarsPage(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	list, err := m.cals.List(r.Context(), v.User.ID)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	cv := calendarsView{Colors: view.Colors(nil, list)}
	for _, c := range list {
		cv.Rows = append(cv.Rows, calendarRow{Calendar: c, Hex: view.Hex(c.Color), Reminders: web.ReminderLabel(c.DefaultReminders)})
	}
	m.site.Render(w, r, http.StatusOK, "settings/calendars", v.Page("settings/calendars", "Calendars", cv))
}

func (m *Module) newCalendar(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	f := web.NewForm(url.Values{"color": {domain.DefaultCalendarColor}, "reminders": {"10"}})
	m.renderCalendarForm(w, r, v, http.StatusOK, f, m.calendarForm(r, calendarsPath, ""), "New calendar")
}

func (m *Module) createCalendar(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	f, ok := m.site.ParseForm(w, r)
	if !ok {
		return
	}
	p, err := calendarPatch(f)
	if err == nil {
		_, err = m.cals.Create(r.Context(), v.User.ID, p)
	}
	if err != nil {
		fv := postedCalendarForm(f, calendarsPath)
		m.site.Retry(w, r, err, f, func(status int) { m.renderCalendarForm(w, r, v, status, f, fv, "New calendar") })
		return
	}
	m.site.Flash(w, r, "ok", "Calendar created.")
	m.site.Finish(w, r, m.site.BackTo(r, f.Get("back")))
}

func (m *Module) editCalendar(w http.ResponseWriter, r *http.Request) {
	v, c, ok := m.loadCalendar(w, r)
	if !ok {
		return
	}
	vals := url.Values{
		"name":        {c.Name},
		"color":       {c.Color},
		"description": {c.Description},
		"timezone":    {c.Timezone},
		"reminders":   {web.JoinInts(c.DefaultReminders)},
	}
	if c.Hidden {
		vals.Set("hidden", "1")
	}
	fv := m.calendarForm(r, calendarsPath+"/"+c.ID.String()+"/edit", c.ETag())
	m.renderCalendarForm(w, r, v, http.StatusOK, web.NewForm(vals), fv, "Edit calendar")
}

func (m *Module) updateCalendar(w http.ResponseWriter, r *http.Request) {
	v, c, ok := m.loadCalendar(w, r)
	if !ok {
		return
	}
	f, ok := m.site.ParseForm(w, r)
	if !ok {
		return
	}
	p, err := calendarPatch(f)
	if err == nil {
		_, err = m.cals.Update(r.Context(), v.User.ID, c.ID, p, f.Get("etag"))
	}
	if err != nil {
		fv := postedCalendarForm(f, calendarsPath+"/"+c.ID.String()+"/edit")
		m.site.Retry(w, r, err, f, func(status int) { m.renderCalendarForm(w, r, v, status, f, fv, "Edit calendar") })
		return
	}
	m.site.Flash(w, r, "ok", "Calendar saved.")
	m.site.Finish(w, r, m.site.BackTo(r, f.Get("back")))
}

func (m *Module) deleteCalendar(w http.ResponseWriter, r *http.Request) {
	v, c, ok := m.loadCalendar(w, r)
	if !ok {
		return
	}
	f, ok := m.site.ParseForm(w, r)
	if !ok {
		return
	}
	if err := m.cals.Delete(r.Context(), v.User.ID, c.ID, f.Get("etag")); err != nil {
		msg := web.ErrorText(err)
		if msg == "" {
			m.site.Fail(w, r, err)
			return
		}
		m.site.Flash(w, r, "error", msg)
	} else {
		m.site.Flash(w, r, "ok", "Calendar deleted.")
	}
	m.site.Finish(w, r, calendarsPath)
}

func (m *Module) loadCalendar(w http.ResponseWriter, r *http.Request) (web.Viewer, domain.Calendar, bool) {
	id, err := domain.ParseID(r.PathValue("id"))
	if err != nil {
		m.site.Fail(w, r, domain.ErrNotFound)
		return web.Viewer{}, domain.Calendar{}, false
	}
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return web.Viewer{}, domain.Calendar{}, false
	}
	c, err := m.cals.Get(r.Context(), v.User.ID, id)
	if err != nil {
		m.site.Fail(w, r, err)
		return web.Viewer{}, domain.Calendar{}, false
	}
	return v, c, true
}

func (m *Module) calendarForm(r *http.Request, action, etag string) calendarFormView {
	fv := calendarFormView{Action: action, Cancel: calendarsPath, ETag: etag}
	if web.IsFragment(r) {
		fv.Back = m.site.BackTo(r, "")
		fv.Cancel = fv.Back
	}
	return fv
}

func postedCalendarForm(f *web.Form, action string) calendarFormView {
	fv := calendarFormView{Action: action, Cancel: calendarsPath, Back: f.Get("back"), ETag: f.Get("etag")}
	if fv.Back != "" {
		fv.Cancel = web.SafeNext(fv.Back)
	}
	return fv
}

func (m *Module) renderCalendarForm(w http.ResponseWriter, r *http.Request, v web.Viewer, status int, f *web.Form, fv calendarFormView, title string) {
	p := v.Page("settings/calendars", title, fv)
	p.Form = f
	m.site.RenderBlock(w, r, status, "settings/calendar_edit", web.Block(r, "calendar_form"), p)
}

func calendarPatch(f *web.Form) (domain.CalendarPatch, error) {
	var v domain.ValidationError
	p := domain.CalendarPatch{
		Name:        domain.Some(f.Get("name")),
		Color:       domain.Some(f.Get("color")),
		Description: domain.Some(f.Get("description")),
		Timezone:    domain.Some(f.Get("timezone")),
		Hidden:      domain.Some(f.Get("hidden") == "1"),
	}
	if mins, ok := web.ParseInts(f.Get("reminders")); ok {
		p.DefaultReminders = domain.Some(mins)
	} else {
		v.Add("default_reminders", "must be minutes separated by commas, like 10, 60")
	}
	return p, v.Err()
}
