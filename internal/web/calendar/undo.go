package calendar

import (
	"context"
	"net/url"

	"github.com/Alexander-D-Karpov/calendar/internal/domain"
	"github.com/Alexander-D-Karpov/calendar/internal/service"
	"github.com/Alexander-D-Karpov/calendar/internal/web"
)

type eventRef struct {
	ID string `json:"id"`
}

type eventSnapshot struct {
	ID       string     `json:"id"`
	Values   url.Values `json:"values"`
	Scope    string     `json:"scope,omitempty"`
	Instance string     `json:"instance,omitempty"`
}

func snapshot(e domain.Event, scope, instance string) eventSnapshot {
	return eventSnapshot{ID: e.ID.String(), Values: formValues(e), Scope: scope, Instance: instance}
}

func (m *Module) ApplyUndo(ctx context.Context, owner domain.ID, entry domain.UndoEntry) error {
	switch entry.Kind {
	case domain.UndoEventCreate:
		var p eventRef
		if err := entry.Decode(&p); err != nil {
			return domain.ErrInvalid
		}
		id, err := domain.ParseID(p.ID)
		if err != nil {
			return domain.ErrNotFound
		}
		return m.events.Delete(ctx, owner, id, service.Edit{Scope: domain.ScopeAll})
	case domain.UndoEventDelete:
		var p eventRef
		if err := entry.Decode(&p); err != nil {
			return domain.ErrInvalid
		}
		id, err := domain.ParseID(p.ID)
		if err != nil {
			return domain.ErrNotFound
		}
		return m.store.Restore(ctx, owner, id, domain.EntityEvent)
	case domain.UndoEventRestore:
		var p eventSnapshot
		if err := entry.Decode(&p); err != nil {
			return domain.ErrInvalid
		}
		id, err := domain.ParseID(p.ID)
		if err != nil {
			return domain.ErrNotFound
		}
		patch, err := eventPatch(web.NewForm(p.Values), p.Scope != domain.ScopeThis, 0)
		if err != nil {
			return err
		}
		_, err = m.events.Update(ctx, owner, id, patch, service.Edit{Scope: p.Scope, Instance: p.Instance})
		return err
	}
	return domain.ErrNotFound
}
