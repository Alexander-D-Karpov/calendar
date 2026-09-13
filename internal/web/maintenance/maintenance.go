package maintenance

import (
	"errors"
	"net/http"
	"slices"

	"github.com/Alexander-D-Karpov/calendar/internal/dedup"
	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

const (
	duplicatesPath = "/settings/duplicates"
	trashPath      = "/settings/trash"
	trashLimit     = 200
)

type Deps struct {
	Site  *web.Server
	Dedup *dedup.Service
	Store Store
}

type Module struct {
	site  *web.Server
	dedup *dedup.Service
	store Store
}

func New(d Deps) *Module {
	return &Module{site: d.Site, dedup: d.Dedup, store: d.Store}
}

func (m *Module) Routes(mux *http.ServeMux) {
	h, user := m.site.Handle, m.site.RequireUser
	mux.Handle("GET "+duplicatesPath, h(m.duplicates, user))
	mux.Handle("POST "+duplicatesPath+"/scan", h(m.scan, user))
	mux.Handle("POST "+duplicatesPath+"/{id}", h(m.resolve, user))
	mux.Handle("GET "+trashPath, h(m.trash, user))
	mux.Handle("POST "+trashPath+"/{entity}/{id}/restore", h(m.restore, user))
	mux.Handle("POST "+trashPath+"/{entity}/{id}/delete", h(m.purge, user))
}

var statuses = []web.Option{
	{Value: domain.DupPending, Label: "To review"},
	{Value: domain.DupMerged, Label: "Merged"},
	{Value: domain.DupDeleted, Label: "Deleted"},
	{Value: domain.DupDismissed, Label: "Kept both"},
}

type duplicatesView struct {
	Pairs    []dedup.Pair
	Status   string
	Statuses []web.Option
	Pending  int
	Policy   string
}

func (m *Module) duplicates(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	status := r.URL.Query().Get("status")
	if web.LabelOf(statuses, status) == "" {
		status = domain.DupPending
	}
	ctx := r.Context()
	pairs, err := m.dedup.List(ctx, v.User.ID, status)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	pending, err := m.dedup.Count(ctx, v.User.ID)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	dv := duplicatesView{Pairs: pairs, Status: status, Statuses: statuses, Pending: pending, Policy: v.User.DedupPolicy}
	m.site.Render(w, r, http.StatusOK, "settings/duplicates", v.Page("settings/duplicates", "Duplicates", dv))
}

func (m *Module) scan(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	if v.User.DedupPolicy == domain.DedupOff {
		m.site.Flash(w, r, "info", "Duplicate checking is off. Turn it on under Profile first.")
		m.site.Redirect(w, r, duplicatesPath)
		return
	}
	if err := m.dedup.Scan(r.Context(), v.User.ID); err != nil {
		m.site.Fail(w, r, err)
		return
	}
	m.site.Flash(w, r, "ok", "Scan started. Reload in a moment to see what it found.")
	m.site.Redirect(w, r, duplicatesPath)
}

func (m *Module) resolve(w http.ResponseWriter, r *http.Request) {
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
	action := f.Get("action")
	if !slices.Contains(dedup.Actions, action) {
		m.site.Fail(w, r, domain.ErrInvalid)
		return
	}
	switch err := m.dedup.Resolve(r.Context(), v.User.ID, id, action); {
	case err == nil:
		m.site.Flash(w, r, "ok", resolved(action))
	case errors.Is(err, domain.ErrNotFound):
		m.site.Flash(w, r, "error", "That pair no longer exists.")
	case web.ErrorText(err) != "":
		m.site.Flash(w, r, "error", web.ErrorText(err))
	default:
		m.site.Fail(w, r, err)
		return
	}
	m.site.Redirect(w, r, duplicatesPath)
}

func resolved(action string) string {
	switch action {
	case dedup.ActionMerge:
		return "Merged into one entry."
	case dedup.ActionDismiss:
		return "Both kept. This pair will not be flagged again."
	}
	return "The copy was moved to the trash."
}

type trashView struct {
	Items []domain.TrashItem
}

func (m *Module) trash(w http.ResponseWriter, r *http.Request) {
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	items, err := m.store.Trash(r.Context(), v.User.ID, trashLimit)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	m.site.Render(w, r, http.StatusOK, "settings/trash", v.Page("settings/trash", "Trash", trashView{Items: items}))
}

func (m *Module) restore(w http.ResponseWriter, r *http.Request) {
	m.act(w, r, func(owner, id domain.ID, entity string) error {
		return m.store.Restore(r.Context(), owner, id, entity)
	}, "Restored.")
}

func (m *Module) purge(w http.ResponseWriter, r *http.Request) {
	m.act(w, r, func(owner, id domain.ID, entity string) error {
		return m.store.PurgeItem(r.Context(), owner, id, entity)
	}, "Deleted for good.")
}

func (m *Module) act(w http.ResponseWriter, r *http.Request, fn func(owner, id domain.ID, entity string) error, ok string) {
	entity := r.PathValue("entity")
	id, err := domain.ParseID(r.PathValue("id"))
	if err != nil || (entity != domain.EntityEvent && entity != domain.EntityTodo) {
		m.site.Fail(w, r, domain.ErrNotFound)
		return
	}
	v, err := m.site.Viewer(r)
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	switch err := fn(v.User.ID, id, entity); {
	case err == nil:
		m.site.Flash(w, r, "ok", ok)
	case errors.Is(err, domain.ErrNotFound):
		m.site.Flash(w, r, "error", "That item is no longer in the trash.")
	case web.ErrorText(err) != "":
		m.site.Flash(w, r, "error", web.ErrorText(err))
	default:
		m.site.Fail(w, r, err)
		return
	}
	m.site.Redirect(w, r, trashPath)
}
