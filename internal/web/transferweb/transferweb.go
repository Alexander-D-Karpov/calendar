package transferweb

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/config"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/exporter"
	"github.com/Alexander-D-Karpov/calendar/internal/importer"
	"github.com/Alexander-D-Karpov/calendar/internal/service"
	"github.com/Alexander-D-Karpov/calendar/internal/subscription"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

const (
	importPath = "/settings/import"
	exportPath = "/settings/export"
	subsPath   = "/settings/subscriptions"
	historyMax = 20
)

type Deps struct {
	Site      *web.Server
	Import    *importer.Service
	Export    *exporter.Service
	Subs      *subscription.Service
	Calendars *service.Calendars
	Lists     *service.TodoLists
}

type Module struct {
	site  *web.Server
	imp   *importer.Service
	exp   *exporter.Service
	subs  *subscription.Service
	cals  *service.Calendars
	lists *service.TodoLists
	cfg   *config.Config
}

func New(d Deps) *Module {
	return &Module{site: d.Site, imp: d.Import, exp: d.Export, subs: d.Subs, cals: d.Calendars, lists: d.Lists, cfg: d.Site.Config()}
}

func (m *Module) Routes(mux *http.ServeMux) {
	h, user := m.site.Handle, m.site.RequireUser
	mux.Handle("GET "+importPath, h(m.importPage, user))
	mux.Handle("POST "+importPath, h(m.upload, user))
	mux.Handle("GET "+importPath+"/{id}", h(m.previewPage, user))
	mux.Handle("POST "+importPath+"/{id}/commit", h(m.commit, user))
	mux.Handle("GET "+exportPath, h(m.exportPage, user))
	mux.Handle("GET "+exportPath+"/download", h(m.download, user))
	mux.Handle("GET "+subsPath, h(m.subsPage, user))
	mux.Handle("POST "+subsPath, h(m.subscribe, user))
	mux.Handle("POST "+subsPath+"/{id}/refresh", h(m.refreshSub, user))
	mux.Handle("POST "+subsPath+"/{id}/delete", h(m.deleteSub, user))
}

type importRow struct {
	domain.Import
	Stats  importer.Stats
	Counts string
}

type importView struct {
	History []importRow
	MaxSize string
}

type targetRow struct {
	importer.Target
	Options []web.Option
	Key     string
	SkipKey string
}

type previewView struct {
	Import   domain.Import
	Preview  importer.Preview
	Targets  []targetRow
	Done     bool
	Stats    importer.Stats
	CommitTo string
}

func (m *Module) importPage(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	m.renderImport(w, r, v, http.StatusOK, web.NewForm(nil))
}

func (m *Module) renderImport(w http.ResponseWriter, r *http.Request, v web.Viewer, status int, f *web.Form) {
	list, err := m.imp.List(r.Context(), v.User.ID, historyMax)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	iv := importView{MaxSize: web.ByteSize(m.cfg.Import.MaxSize.Int64())}
	for _, im := range list {
		row := importRow{Import: im}
		if len(im.Stats) > 0 {
			_ = json.Unmarshal(im.Stats, &row.Stats)
			row.Counts = counts(row.Stats)
		}
		iv.History = append(iv.History, row)
	}
	p := v.Page("settings/import", "Import", iv)
	p.Form = f
	m.site.Render(w, r, status, "settings/import", p)
}

func counts(s importer.Stats) string {
	parts := []string{}
	add := func(n int, label string) {
		if n > 0 {
			parts = append(parts, strconv.Itoa(n)+" "+label)
		}
	}
	add(s.New, "added")
	add(s.Updated, "updated")
	add(s.Duplicate, "duplicate")
	add(s.Skipped, "skipped")
	add(s.Invalid, "invalid")
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, ", ")
}

func (m *Module) upload(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	up, f, ok := m.site.Upload(w, r, "file", m.cfg.Import.MaxSize.Int64())
	if !ok {
		if f != nil {
			m.renderImport(w, r, v, http.StatusUnprocessableEntity, f)
		}
		return
	}
	im, _, err := m.imp.Upload(r.Context(), v.User.ID, up.Filename, up.Body)
	if err != nil {
		if msg := web.ErrorText(err); msg != "" {
			f.Errors["file"] = msg
			m.renderImport(w, r, v, http.StatusUnprocessableEntity, f)
			return
		}
		m.site.Fail(w, r, err)
		return
	}
	m.site.Redirect(w, r, importPath+"/"+im.ID.String())
}

func (m *Module) previewPage(w http.ResponseWriter, r *http.Request) {
	v, im, p, ok := m.load(w, r)
	if !ok {
		return
	}
	m.renderPreview(w, r, v, im, p, http.StatusOK, web.NewForm(nil))
}

func (m *Module) renderPreview(w http.ResponseWriter, r *http.Request, v web.Viewer, im domain.Import, p importer.Preview, status int, f *web.Form) {
	ctx := r.Context()
	cals, err := m.cals.List(ctx, v.User.ID)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	lists, err := m.lists.List(ctx, v.User.ID)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	calOpts := []web.Option{{Value: importer.TargetNew, Label: "New calendar"}}
	for _, c := range cals {
		if !c.ReadOnly {
			calOpts = append(calOpts, web.Option{Value: c.ID.String(), Label: c.Name})
		}
	}
	listOpts := []web.Option{{Value: importer.TargetNew, Label: "New list"}}
	for _, l := range lists {
		listOpts = append(listOpts, web.Option{Value: l.ID.String(), Label: l.Name})
	}
	pv := previewView{Import: im, Preview: p, Done: im.Status != domain.ImportPreviewed, CommitTo: importPath + "/" + im.ID.String() + "/commit"}
	if len(im.Stats) > 0 {
		_ = json.Unmarshal(im.Stats, &pv.Stats)
	}
	for _, t := range p.Targets {
		row := targetRow{Target: t, Options: calOpts, Key: "target_" + strconv.Itoa(t.Index), SkipKey: "skip_" + strconv.Itoa(t.Index)}
		if t.Kind == domain.BindTaskList {
			row.Options = listOpts
		}
		if !slices.ContainsFunc(row.Options, func(o web.Option) bool { return o.Value == t.Target }) {
			row.Target.Target = importer.TargetNew
		}
		if f.Values.Get(row.Key) == "" {
			f.Values.Set(row.Key, row.Target.Target)
		}
		pv.Targets = append(pv.Targets, row)
	}
	page := v.Page("settings/import", "Review import", pv)
	page.Form = f
	m.site.Render(w, r, status, "settings/import_preview", page)
}

func (m *Module) commit(w http.ResponseWriter, r *http.Request) {
	v, im, p, ok := m.load(w, r)
	if !ok {
		return
	}
	f, ok := m.site.ParseForm(w, r)
	if !ok {
		return
	}
	c := importer.Commit{}
	for _, t := range p.Targets {
		c.Targets = append(c.Targets, importer.Choice{
			Index:  t.Index,
			Target: f.Get("target_" + strconv.Itoa(t.Index)),
			Skip:   f.Get("skip_"+strconv.Itoa(t.Index)) == "1",
		})
	}
	stats, err := m.imp.Commit(r.Context(), v.User.ID, im.ID, c)
	if err != nil {
		if msg := web.ErrorText(err); msg != "" {
			f.Error = msg
			m.renderPreview(w, r, v, im, p, web.StatusFor(err), f)
			return
		}
		m.site.Fail(w, r, err)
		return
	}
	m.site.Flash(w, r, "ok", "Import finished: "+counts(stats)+".")
	m.site.Redirect(w, r, importPath+"/"+im.ID.String())
}

func (m *Module) load(w http.ResponseWriter, r *http.Request) (web.Viewer, domain.Import, importer.Preview, bool) {
	id, err := domain.ParseID(r.PathValue("id"))
	if err != nil {
		m.site.Fail(w, r, domain.ErrNotFound)
		return web.Viewer{}, domain.Import{}, importer.Preview{}, false
	}
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return web.Viewer{}, domain.Import{}, importer.Preview{}, false
	}
	im, p, err := m.imp.Get(r.Context(), v.User.ID, id)
	if err != nil {
		m.site.Fail(w, r, err)
		return web.Viewer{}, domain.Import{}, importer.Preview{}, false
	}
	return v, im, p, true
}

type exportView struct {
	Calendars []domain.Calendar
	Lists     []domain.TodoList
}

func (m *Module) exportPage(w http.ResponseWriter, r *http.Request) {
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
	lists, err := m.lists.List(ctx, v.User.ID)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	m.site.Render(w, r, http.StatusOK, "settings/export", v.Page("settings/export", "Export", exportView{Calendars: cals, Lists: lists}))
}

func (m *Module) download(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	q := r.URL.Query()
	ids, err := parseIDs(q["cal"])
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	var file exporter.File
	switch q.Get("format") {
	case "ics":
		file, err = m.exp.ICS(r.Context(), v.User.ID, ids)
	case "csv":
		file, err = m.exp.CSV(r.Context(), v.User.ID, ids)
	case "yaml":
		file, err = m.exp.YAML(r.Context(), v.User.ID)
	default:
		m.site.Fail(w, r, domain.ErrNotFound)
		return
	}
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	m.site.SendFile(w, file.Name, file.ContentType, file.Body)
}

func parseIDs(raw []string) ([]domain.ID, error) {
	var out []domain.ID
	for _, v := range raw {
		id, err := domain.ParseID(v)
		if err != nil {
			return nil, domain.ErrNotFound
		}
		out = append(out, id)
	}
	return out, nil
}

type subsView struct {
	Rows     []subscription.Status
	MinEvery string
}

func (m *Module) subsPage(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	m.renderSubs(w, r, v, http.StatusOK, web.NewForm(url.Values{"every": {m.cfg.Subscription.DefaultInterval.String()}}))
}

func (m *Module) renderSubs(w http.ResponseWriter, r *http.Request, v web.Viewer, status int, f *web.Form) {
	rows, err := m.subs.List(r.Context(), v.User.ID)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	p := v.Page("settings/subscriptions", "Subscriptions", subsView{Rows: rows, MinEvery: m.cfg.Subscription.MinInterval.String()})
	p.Form = f
	m.site.Render(w, r, status, "settings/subscriptions", p)
}

func (m *Module) subscribe(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	f, ok := m.site.ParseForm(w, r)
	if !ok {
		return
	}
	every, perr := time.ParseDuration(f.Get("every"))
	if f.Get("every") == "" {
		every, perr = 0, nil
	}
	if perr != nil {
		f.Errors["every"] = "Use a duration like 1h or 30m."
		m.renderSubs(w, r, v, http.StatusUnprocessableEntity, f)
		return
	}
	if _, err := m.subs.Create(r.Context(), v.User.ID, f.Get("url"), f.Get("name"), every); err != nil {
		if msg := web.ErrorText(err); msg != "" {
			m.site.Retry(w, r, err, f, func(status int) { m.renderSubs(w, r, v, status, f) })
			return
		}
		m.site.Fail(w, r, err)
		return
	}
	m.site.Flash(w, r, "ok", "Subscribed. The first refresh runs in the background.")
	m.site.Redirect(w, r, subsPath)
}

func (m *Module) refreshSub(w http.ResponseWriter, r *http.Request) {
	m.act(w, r, func(owner, id domain.ID, r *http.Request) error {
		return m.subs.Refresh(r.Context(), owner, id)
	}, "Refresh started.")
}

func (m *Module) deleteSub(w http.ResponseWriter, r *http.Request) {
	m.act(w, r, func(owner, id domain.ID, r *http.Request) error {
		return m.subs.Delete(r.Context(), owner, id)
	}, "Subscription removed.")
}

func (m *Module) act(w http.ResponseWriter, r *http.Request, fn func(owner, id domain.ID, r *http.Request) error, ok string) {
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
	switch err := fn(v.User.ID, id, r); {
	case err == nil:
		m.site.Flash(w, r, "ok", ok)
	case errors.Is(err, domain.ErrNotFound):
		m.site.Flash(w, r, "error", "That subscription no longer exists.")
	case web.ErrorText(err) != "":
		m.site.Flash(w, r, "error", web.ErrorText(err))
	default:
		m.site.Fail(w, r, err)
		return
	}
	m.site.Redirect(w, r, subsPath)
}
