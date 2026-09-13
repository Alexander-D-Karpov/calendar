package undoweb

import (
	"context"
	"errors"
	"net/http"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/undo"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

type Applier interface {
	ApplyUndo(ctx context.Context, owner domain.ID, entry domain.UndoEntry) error
}

type Deps struct {
	Site   *web.Server
	Undo   *undo.Service
	Events Applier
	Todos  Applier
}

type Module struct {
	site   *web.Server
	undo   *undo.Service
	events Applier
	todos  Applier
}

func New(d Deps) *Module {
	return &Module{site: d.Site, undo: d.Undo, events: d.Events, todos: d.Todos}
}

func (m *Module) Routes(mux *http.ServeMux) {
	mux.Handle("POST /undo/{id}", m.site.Handle(m.apply, m.site.RequireUser))
}

func (m *Module) apply(w http.ResponseWriter, r *http.Request) {
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
	back := m.site.BackTo(r, "")
	entry, err := m.undo.Take(r.Context(), v.User.ID, id)
	if errors.Is(err, domain.ErrNotFound) {
		m.site.Flash(w, r, "info", "That change can no longer be undone.")
		m.site.Finish(w, r, back)
		return
	}
	if err != nil {
		m.site.Fail(w, r, err)
		return
	}
	var applier Applier
	switch entry.Entity() {
	case domain.EntityEvent:
		applier = m.events
	case domain.EntityTodo:
		applier = m.todos
	}
	if applier == nil {
		m.site.Fail(w, r, domain.ErrNotFound)
		return
	}
	switch err := applier.ApplyUndo(r.Context(), v.User.ID, entry); {
	case err == nil:
		m.site.Flash(w, r, "ok", "Undone.")
	case errors.Is(err, domain.ErrNotFound):
		m.site.Flash(w, r, "error", "The item is gone, so this cannot be undone.")
	case web.ErrorText(err) != "":
		m.site.Flash(w, r, "error", web.ErrorText(err))
	default:
		m.site.Fail(w, r, err)
		return
	}
	m.site.Finish(w, r, back)
}
