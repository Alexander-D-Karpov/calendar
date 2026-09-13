package googlesync

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/Alexander-D-Karpov/calendar/internal/auth"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/google"
	"github.com/Alexander-D-Karpov/calendar/internal/gsync"
	"github.com/Alexander-D-Karpov/calendar/internal/service"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

const (
	path    = "/settings/google"
	maxRows = 500
)

var directions = []web.Option{
	{Value: domain.SyncBoth, Label: "Both ways"},
	{Value: domain.SyncPull, Label: "From Google"},
	{Value: domain.SyncPush, Label: "To Google"},
}

const maxConflicts = 20

type ConflictStore interface {
	Conflicts(ctx context.Context, owner domain.ID, limit int) ([]domain.SyncConflict, error)
}

type Deps struct {
	Site      *web.Server
	Engine    *gsync.Engine
	Calendars *service.Calendars
	Lists     *service.TodoLists
	Store     ConflictStore
}

type Module struct {
	site   *web.Server
	engine *gsync.Engine
	cals   *service.Calendars
	lists  *service.TodoLists
	store  ConflictStore
}

func New(d Deps) *Module {
	return &Module{site: d.Site, engine: d.Engine, cals: d.Calendars, lists: d.Lists, store: d.Store}
}

func (m *Module) Routes(mux *http.ServeMux) {
	h, user, fresh := m.site.Handle, m.site.RequireUser, m.site.RequireFresh
	mux.Handle("GET "+path, h(m.page, user))
	mux.Handle("POST "+path, h(m.save, user))
	mux.Handle("POST "+path+"/run", h(m.run, user))
	mux.Handle("POST "+path+"/disconnect", h(m.disconnect, user, fresh))
}

type row struct {
	Index      int
	Entity     string
	RemoteID   string
	Name       string
	Kind       string
	ReadOnly   bool
	Missing    bool
	Target     string
	Direction  string
	Targets    []web.Option
	Bound      bool
	Pending    int
	Watching   bool
	LastSynced *time.Time
	Error      string
}

func (r row) Key(field string) string {
	return fmt.Sprintf("%s_%d", field, r.Index)
}

type pageView struct {
	Account    *domain.GoogleAccount
	Rows       []row
	Directions []web.Option
	Conflicts  []domain.SyncConflict
	Error      string
	Fresh      bool
}

func (m *Module) page(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	m.render(w, r, v, http.StatusOK, nil)
}

func (m *Module) render(w http.ResponseWriter, r *http.Request, v web.Viewer, status int, f *web.Form) {
	ctx := r.Context()
	st, err := m.engine.Status(ctx, v.User.ID)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	pv := pageView{Account: st.Account, Directions: directions, Fresh: auth.PrincipalFrom(ctx).Fresh(v.Now)}
	if st.Account != nil && st.Account.Active() {
		rem, err := m.engine.Remotes(ctx, v.User.ID)
		switch {
		case errors.Is(err, google.ErrRevoked):
			pv.Error = "Google access was revoked. Reconnect to continue."
		case err != nil:
			pv.Error = "Google could not be reached. Try again in a moment."
		default:
			if pv.Rows, err = m.rows(ctx, v.User.ID, rem, st.Bindings); err != nil {
				m.site.Fail(w, r, err)
				return
			}
		}
	}
	if m.store != nil {
		if c, err := m.store.Conflicts(ctx, v.User.ID, maxConflicts); err == nil {
			pv.Conflicts = c
		}
	}
	if f == nil {
		f = web.NewForm(nil)
		for _, rw := range pv.Rows {
			f.Values.Set(rw.Key("target"), rw.Target)
			f.Values.Set(rw.Key("dir"), rw.Direction)
		}
	}
	f.Values.Set("rows", strconv.Itoa(len(pv.Rows)))
	p := v.Page("settings/google", "Google", pv)
	p.Form = f
	m.site.Render(w, r, status, "settings/google", p)
}

func (m *Module) rows(ctx context.Context, owner domain.ID, rem gsync.Remotes, bs []gsync.BindingStatus) ([]row, error) {
	cals, err := m.cals.List(ctx, owner)
	if err != nil {
		return nil, err
	}
	lists, err := m.lists.List(ctx, owner)
	if err != nil {
		return nil, err
	}
	calOpts := []web.Option{{Value: "", Label: "Don't sync"}, {Value: gsync.TargetNew, Label: "New calendar"}}
	for _, c := range cals {
		calOpts = append(calOpts, web.Option{Value: c.ID.String(), Label: c.Name})
	}
	listOpts := []web.Option{{Value: "", Label: "Don't sync"}, {Value: gsync.TargetNew, Label: "New list"}}
	for _, l := range lists {
		listOpts = append(listOpts, web.Option{Value: l.ID.String(), Label: l.Name})
	}
	bound := make(map[string]gsync.BindingStatus, len(bs))
	for _, b := range bs {
		bound[b.Entity+"\x00"+b.RemoteID] = b
	}
	var out []row
	add := func(entity, id, name, kind string, readOnly, missing bool, opts []web.Option) {
		rw := row{Index: len(out), Entity: entity, RemoteID: id, Name: name, Kind: kind, ReadOnly: readOnly, Missing: missing, Targets: opts, Direction: domain.SyncBoth}
		if readOnly {
			rw.Direction = domain.SyncPull
		}
		key := entity + "\x00" + id
		if b, ok := bound[key]; ok {
			rw.Bound, rw.Target, rw.Direction = true, b.LocalID().String(), b.Direction
			rw.Pending, rw.Watching, rw.LastSynced, rw.Error = b.Pending, b.Watching, b.LastSyncedAt, b.LastError
			delete(bound, key)
		}
		out = append(out, rw)
	}
	for _, c := range rem.Calendars {
		name := c.Name
		if c.Primary {
			name += " (primary)"
		}
		add(domain.BindCalendar, c.ID, name, "Calendar", c.ReadOnly(), false, calOpts)
	}
	for _, l := range rem.Lists {
		add(domain.BindTaskList, l.ID, l.Name, "Task list", false, false, listOpts)
	}
	for _, b := range bs {
		if _, ok := bound[b.Entity+"\x00"+b.RemoteID]; !ok {
			continue
		}
		kind := "Calendar"
		if b.Entity == domain.BindTaskList {
			kind = "Task list"
		}
		opts := []web.Option{{Value: b.LocalID().String(), Label: "Keep for now"}, {Value: "", Label: "Stop syncing"}}
		add(b.Entity, b.RemoteID, b.RemoteName, kind, b.Direction == domain.SyncPull, true, opts)
	}
	return out, nil
}

func (m *Module) save(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	f, ok := m.site.ParseForm(w, r)
	if !ok {
		return
	}
	n, _ := strconv.Atoi(f.Get("rows"))
	n = min(max(n, 0), maxRows)
	choices := make([]gsync.Choice, 0, n)
	for i := range n {
		key := func(field string) string { return fmt.Sprintf("%s_%d", field, i) }
		choices = append(choices, gsync.Choice{
			Entity: f.Get(key("entity")), RemoteID: f.Get(key("remote")), Target: f.Get(key("target")), Direction: f.Get(key("dir")),
		})
	}
	err = m.engine.Apply(r.Context(), v.User.ID, choices)
	switch {
	case err == nil:
		m.site.Flash(w, r, "ok", "Sync settings saved. Entries sync in the background.")
	case errors.Is(err, google.ErrRevoked), errors.Is(err, gsync.ErrNotConnected):
		m.site.Flash(w, r, "error", "Google is not connected. Connect it and try again.")
	default:
		m.site.Retry(w, r, err, f, func(status int) { m.render(w, r, v, status, f) })
		return
	}
	m.site.Redirect(w, r, path)
}

func (m *Module) run(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	if err := m.engine.Run(r.Context(), v.User.ID); err != nil {
		m.site.Fail(w, r, err)
		return
	}
	m.site.Flash(w, r, "ok", "Sync started.")
	m.site.Redirect(w, r, path)
}

func (m *Module) disconnect(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	f, ok := m.site.ParseForm(w, r)
	if !ok {
		return
	}
	err = m.engine.Disconnect(r.Context(), v.User.ID, f.Get("keep") != "0")
	switch {
	case err == nil:
		m.site.Flash(w, r, "ok", "Google disconnected.")
	case errors.Is(err, gsync.ErrNotConnected):
		m.site.Flash(w, r, "info", "Google was not connected.")
	default:
		m.site.Fail(w, r, err)
		return
	}
	m.site.Redirect(w, r, path)
}
