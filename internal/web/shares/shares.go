package shares

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/exporter"
	"github.com/Alexander-D-Karpov/calendar/internal/metrics"
	"github.com/Alexander-D-Karpov/calendar/internal/og"
	"github.com/Alexander-D-Karpov/calendar/internal/ratelimit"
	"github.com/Alexander-D-Karpov/calendar/internal/realtime"
	"github.com/Alexander-D-Karpov/calendar/internal/service"
	"github.com/Alexander-D-Karpov/calendar/internal/view"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
	"github.com/Alexander-D-Karpov/calendar/internal/web/timeline"
)

const listPath = "/settings/shares"

var (
	detailOptions = []web.Option{{Value: domain.DetailBusy, Label: "Busy only"}, {Value: domain.DetailTitles, Label: "Titles"}, {Value: domain.DetailFull, Label: "Full details"}}
	tzOptions     = []web.Option{{Value: domain.ShareTZOwner, Label: "My timezone"}, {Value: domain.ShareTZViewer, Label: "Viewer's timezone"}}
)

type Deps struct {
	Site      *web.Server
	Shares    *service.Shares
	Calendars *service.Calendars
	Source    timeline.Source
	Hub       *realtime.Hub
	Export    *exporter.Service
	OG        *og.Renderer
	OGCache   *og.Cache
	Stamps    StampStore
	Metrics   *metrics.Metrics
	Limiter   *ratelimit.Limiter
}

type Module struct {
	site    *web.Server
	shares  *service.Shares
	cals    *service.Calendars
	src     timeline.Source
	hub     *realtime.Hub
	export  *exporter.Service
	og_     *og.Renderer
	cache   *og.Cache
	stamps  StampStore
	metrics *metrics.Metrics
	limiter *ratelimit.Limiter
}

func New(d Deps) *Module {
	return &Module{
		site: d.Site, shares: d.Shares, cals: d.Calendars, src: d.Source, hub: d.Hub, export: d.Export,
		og_: d.OG, cache: d.OGCache, stamps: d.Stamps, metrics: d.Metrics, limiter: d.Limiter,
	}
}

func (m *Module) Routes(mux *http.ServeMux) {
	h, user := m.site.Handle, m.site.RequireUser
	limit := ratelimit.Middleware(m.limiter, ratelimit.ByIP, m.site.Fail)
	mux.Handle("GET "+listPath, h(m.index, user))
	mux.Handle("GET "+listPath+"/new", h(m.newShare, user))
	mux.Handle("POST "+listPath, h(m.create, user))
	mux.Handle("GET "+listPath+"/{id}/edit", h(m.edit, user))
	mux.Handle("POST "+listPath+"/{id}/edit", h(m.update, user))
	mux.Handle("POST "+listPath+"/{id}/regenerate", h(m.regenerate, user))
	mux.Handle("POST "+listPath+"/{id}/revoke", h(m.revoke, user))
	mux.Handle("POST "+listPath+"/quick", h(m.quick, user))
	mux.Handle("GET /s/{token}", h(m.public, limit))
	mux.Handle("GET /s/{token}/ws", h(m.publicLive, limit))
	mux.Handle("GET /s/{token}/og.png", h(m.og, limit))
}

type shareRow struct {
	domain.Share
	URL           string
	FeedURL       string
	Summary       string
	DetailLabel   string
	CalendarNames string
}

type indexView struct {
	Rows []shareRow
}

type calOption struct {
	ID      string
	Name    string
	Checked bool
}

type formView struct {
	Action    string
	Cancel    string
	Back      string
	ETag      string
	IsNew     bool
	Summary   string
	Views     []web.Option
	Details   []web.Option
	TZModes   []web.Option
	Calendars []calOption
}

func (m *Module) index(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	ctx := r.Context()
	list, err := m.shares.List(ctx, v.User.ID)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	cals, err := m.cals.List(ctx, v.User.ID)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	names := make(map[domain.ID]string, len(cals))
	for _, c := range cals {
		names[c.ID] = c.Name
	}
	ws := time.Weekday(v.User.WeekStart)
	iv := indexView{}
	for _, s := range list {
		row := shareRow{Share: s, Summary: summary(s, ws), DetailLabel: web.LabelOf(detailOptions, s.Detail)}
		if s.Token != "" {
			row.URL = m.site.Config().App.URL("/s/" + s.Token)
			row.FeedURL = row.URL + ".ics"
		}
		var parts []string
		for _, id := range s.Calendars {
			if n, ok := names[id]; ok {
				parts = append(parts, n)
			}
		}
		row.CalendarNames = strings.Join(parts, ", ")
		iv.Rows = append(iv.Rows, row)
	}
	m.site.Render(w, r, http.StatusOK, "settings/shares", v.Page("settings/shares", "Share links", iv))
}

func (m *Module) newShare(w http.ResponseWriter, r *http.Request) {
	v, cals, ok := m.load(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	k, ok := view.ParseKind(q.Get("view"))
	if !ok {
		k = view.Week
	}
	day, ok := view.ParseDate(q.Get("period"))
	if !ok {
		day = v.Today()
	}
	p := view.NewPeriod(k, day, time.Weekday(v.User.WeekStart))
	vals := url.Values{
		"name":    {k.Label() + " · " + p.Title()},
		"view":    {string(k)},
		"period":  {p.Origin().Format(domain.DateLayout)},
		"detail":  {domain.DetailTitles},
		"tz_mode": {domain.ShareTZOwner},
		"sleep":   {"1"},
	}
	if q.Get("todos") == "1" {
		vals.Set("todos", "1")
	}
	selected := q["cal"]
	if len(selected) == 0 {
		for _, c := range cals {
			if !c.Hidden {
				selected = append(selected, c.ID.String())
			}
		}
	}
	vals["cal"] = selected
	fv := formView{Action: listPath, Cancel: listPath, IsNew: true}
	if web.IsFragment(r) {
		fv.Back = m.site.BackTo(r, "")
		fv.Cancel = fv.Back
	}
	m.renderForm(w, r, v, cals, http.StatusOK, web.NewForm(vals), fv, "Share link")
}

func (m *Module) quick(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	f, ok := m.site.ParseForm(w, r)
	if !ok {
		return
	}
	k, kok := view.ParseKind(f.Get("view"))
	if !kok {
		k = view.Week
	}
	day, dok := view.ParseDate(f.Get("period"))
	if !dok {
		day = v.Today()
	}
	p := view.NewPeriod(k, day, time.Weekday(v.User.WeekStart))
	cals := f.Values["cal"]
	if cals == nil {
		cals = []string{}
	}
	sh, err := m.shares.Create(r.Context(), v.User.ID, domain.SharePatch{
		Name:         domain.Some(k.Label() + " · " + p.Title()),
		View:         domain.Some(string(k)),
		Period:       domain.Some(p.Origin().Format(domain.DateLayout)),
		Calendars:    domain.Some(cals),
		Detail:       domain.Some(domain.DetailTitles),
		TZMode:       domain.Some(domain.ShareTZOwner),
		IncludeTodos: domain.Some(f.Get("todos") == "1"),
		ShowSleep:    domain.Some(true),
	})
	if err != nil {
		msg := web.ErrorText(err)
		if msg == "" {
			m.site.Fail(w, r, err)
			return
		}
		m.site.Flash(w, r, "error", msg)
		m.site.Finish(w, r, m.site.BackTo(r, ""))
		return
	}
	link := m.site.Config().App.URL("/s/" + sh.Token)
	if web.IsFragment(r) {
		h := w.Header()
		h.Set("X-Share-URL", link)
		h.Set("X-Share-Edit", listPath+"/"+sh.ID.String()+"/edit")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	m.site.Flash(w, r, "ok", "Share link created.")
	m.site.Redirect(w, r, listPath+"#share-"+sh.ID.String())
}

func (m *Module) create(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	f, ok := m.site.ParseForm(w, r)
	if !ok {
		return
	}
	if k, ok := view.ParseKind(f.Get("view")); ok {
		if d, ok := view.ParseDate(f.Get("period")); ok {
			f.Values.Set("period", view.NewPeriod(k, d, time.Weekday(v.User.WeekStart)).Origin().Format(domain.DateLayout))
		}
	}
	sh, err := m.shares.Create(r.Context(), v.User.ID, sharePatch(f, true))
	if err != nil {
		cals, lerr := m.cals.List(r.Context(), v.User.ID)
		if lerr != nil {
			m.site.Fail(w, r, lerr)
			return
		}
		fv := formView{Action: listPath, Cancel: listPath, Back: f.Get("back"), IsNew: true}
		if fv.Back != "" {
			fv.Cancel = web.SafeNext(fv.Back)
		}
		m.site.Retry(w, r, err, f, func(status int) { m.renderForm(w, r, v, cals, status, f, fv, "Share link") })
		return
	}
	m.site.Flash(w, r, "ok", "Share link created.")
	m.site.Finish(w, r, listPath+"#share-"+sh.ID.String())
}

func (m *Module) edit(w http.ResponseWriter, r *http.Request) {
	v, sh, ok := m.loadShare(w, r)
	if !ok {
		return
	}
	cals, err := m.cals.List(r.Context(), v.User.ID)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	vals := url.Values{"name": {sh.Name}, "detail": {sh.Detail}, "tz_mode": {sh.TZMode}}
	for _, id := range sh.Calendars {
		vals.Add("cal", id.String())
	}
	if sh.IncludeTodos {
		vals.Set("todos", "1")
	}
	if sh.ShowSleep {
		vals.Set("sleep", "1")
	}
	m.renderForm(w, r, v, cals, http.StatusOK, web.NewForm(vals), m.editView(r, v, sh, sh.ETag()), "Edit share link")
}

func (m *Module) update(w http.ResponseWriter, r *http.Request) {
	v, sh, ok := m.loadShare(w, r)
	if !ok {
		return
	}
	f, ok := m.site.ParseForm(w, r)
	if !ok {
		return
	}
	if _, err := m.shares.Update(r.Context(), v.User.ID, sh.ID, sharePatch(f, false), f.Get("etag")); err != nil {
		cals, lerr := m.cals.List(r.Context(), v.User.ID)
		if lerr != nil {
			m.site.Fail(w, r, lerr)
			return
		}
		fv := m.editView(r, v, sh, f.Get("etag"))
		m.site.Retry(w, r, err, f, func(status int) { m.renderForm(w, r, v, cals, status, f, fv, "Edit share link") })
		return
	}
	m.site.Flash(w, r, "ok", "Share link saved.")
	m.site.Finish(w, r, listPath)
}

func (m *Module) regenerate(w http.ResponseWriter, r *http.Request) {
	v, sh, ok := m.loadShare(w, r)
	if !ok {
		return
	}
	if _, err := m.shares.Regenerate(r.Context(), v.User.ID, sh.ID); err != nil {
		m.site.Fail(w, r, err)
		return
	}
	m.site.Flash(w, r, "ok", "The link has a new address. The old one no longer works.")
	m.site.Finish(w, r, listPath+"#share-"+sh.ID.String())
}

func (m *Module) revoke(w http.ResponseWriter, r *http.Request) {
	v, sh, ok := m.loadShare(w, r)
	if !ok {
		return
	}
	if err := m.shares.Revoke(r.Context(), v.User.ID, sh.ID); err != nil {
		m.site.Fail(w, r, err)
		return
	}
	m.site.Flash(w, r, "ok", "The link was revoked.")
	m.site.Finish(w, r, listPath)
}

func (m *Module) editView(r *http.Request, v web.Viewer, sh domain.Share, etag string) formView {
	fv := formView{
		Action:  listPath + "/" + sh.ID.String() + "/edit",
		Cancel:  listPath,
		ETag:    etag,
		Summary: summary(sh, time.Weekday(v.User.WeekStart)),
	}
	if web.IsFragment(r) {
		fv.Back = m.site.BackTo(r, "")
		fv.Cancel = fv.Back
	}
	return fv
}

func (m *Module) load(w http.ResponseWriter, r *http.Request) (web.Viewer, []domain.Calendar, bool) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return web.Viewer{}, nil, false
	}
	cals, err := m.cals.List(r.Context(), v.User.ID)
	if err != nil {
		m.site.Fail(w, r, err)
		return web.Viewer{}, nil, false
	}
	return v, cals, true
}

func (m *Module) loadShare(w http.ResponseWriter, r *http.Request) (web.Viewer, domain.Share, bool) {
	id, err := domain.ParseID(r.PathValue("id"))
	if err != nil {
		m.site.Fail(w, r, domain.ErrNotFound)
		return web.Viewer{}, domain.Share{}, false
	}
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return web.Viewer{}, domain.Share{}, false
	}
	sh, err := m.shares.Get(r.Context(), v.User.ID, id)
	if err != nil {
		m.site.Fail(w, r, err)
		return web.Viewer{}, domain.Share{}, false
	}
	return v, sh, true
}

func (m *Module) renderForm(w http.ResponseWriter, r *http.Request, v web.Viewer, cals []domain.Calendar, status int, f *web.Form, fv formView, title string) {
	fv.Views, fv.Details, fv.TZModes = viewOptions(), detailOptions, tzOptions
	for _, c := range cals {
		id := c.ID.String()
		fv.Calendars = append(fv.Calendars, calOption{ID: id, Name: c.Name, Checked: f.Has("cal", id)})
	}
	p := v.Page("settings/shares", title, fv)
	p.Form = f
	m.site.RenderBlock(w, r, status, "settings/share_edit", web.Block(r, "share_form"), p)
}

func sharePatch(f *web.Form, isNew bool) domain.SharePatch {
	cals := f.Values["cal"]
	if cals == nil {
		cals = []string{}
	}
	p := domain.SharePatch{
		Name:         domain.Some(f.Get("name")),
		Calendars:    domain.Some(cals),
		Detail:       domain.Some(f.Get("detail")),
		TZMode:       domain.Some(f.Get("tz_mode")),
		IncludeTodos: domain.Some(f.Get("todos") == "1"),
		ShowSleep:    domain.Some(f.Get("sleep") == "1"),
	}
	if isNew {
		p.View = domain.Some(f.Get("view"))
		p.Period = domain.Some(f.Get("period"))
	}
	return p
}

func summary(s domain.Share, ws time.Weekday) string {
	k, ok := view.ParseKind(s.View)
	if !ok {
		return s.View
	}
	return k.Label() + " · " + view.NewPeriod(k, s.Period, ws).Title()
}

func viewOptions() []web.Option {
	out := make([]web.Option, len(view.Kinds))
	for i, k := range view.Kinds {
		out[i] = web.Option{Value: string(k), Label: k.Label()}
	}
	return out
}
